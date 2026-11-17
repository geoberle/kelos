// Package merge reconciles commit-bound human merge authorization.
package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v80/github"

	"github.com/hearthctl/kelos-orchestrator/internal/feedback"
	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

// Poll considers only enrolled open PRs in explicitly configured repositories.
// Controller must be a dedicated identity, distinct from the authorized human.
func Poll(ctx context.Context, c *github.Client, cache *review.Cache, repositories []string, controller string) error {
	if len(controller) == 0 || controller == "geoberle" {
		return errors.New("dedicated controller login required")
	}
	var failures []error
	for _, repository := range repositories {
		owner, repo, ok := strings.Cut(repository, "/")
		if !ok || len(owner) == 0 || len(repo) == 0 {
			return errors.New("invalid repository")
		}
		opts := &github.PullRequestListOptions{State: "open", ListOptions: github.ListOptions{PerPage: 100}}
		// Per base branch: whether a verify run is under way, and the oldest
		// pull request without a verify result for the current base.
		verifying := map[string]bool{}
		unverified := map[string]*github.PullRequest{}
		for {
			prs, response, err := c.PullRequests.List(ctx, owner, repo, opts)
			if err != nil {
				failures = append(failures, err)
				break
			}
			for _, pr := range prs {
				if !label(pr, "agent-managed") {
					continue
				}
				err := reconcile(ctx, c, cache, owner, repo, pr.GetNumber(), controller)
				branch := pr.GetBase().GetRef()
				switch {
				case errors.Is(err, errVerifying):
					verifying[branch] = true
				case errors.Is(err, errUnverified):
					if oldest := unverified[branch]; oldest == nil || pr.GetNumber() < oldest.GetNumber() {
						unverified[branch] = pr
					}
				case err != nil:
					failures = append(failures, fmt.Errorf("%s#%d: %w", repository, pr.GetNumber(), err))
				}
			}
			if response.NextPage == 0 {
				break
			}
			opts.Page = response.NextPage
		}
		// As Tide does, verify one pull request per base branch at a time:
		// each merge moves the base and would outdate every other run.
		for branch, pr := range unverified {
			if verifying[branch] {
				continue
			}
			if err := retest(ctx, c, owner, repo, pr); err != nil {
				failures = append(failures, fmt.Errorf("%s#%d: %w", repository, pr.GetNumber(), err))
			}
		}
	}
	return errors.Join(failures...)
}

func label(pr *github.PullRequest, name string) bool {
	for _, l := range pr.Labels {
		if l.GetName() == name {
			return true
		}
	}
	return false
}

func reconcile(ctx context.Context, c *github.Client, cache *review.Cache, owner, repo string, number int, controller string) error {
	pr, _, err := c.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	if pr.GetMerged() {
		return nil
	}
	if err := reportConflict(ctx, c, owner, repo, pr); err != nil {
		return err
	}
	approval, err := authorize(ctx, c, cache, owner, repo, pr, controller)
	// An unfinished review run decides nothing: keep the approval, merge later.
	if errors.Is(err, review.ErrWaiting) {
		return nil
	}
	if err != nil || approval == nil {
		return errors.Join(err, withdraw(ctx, c, owner, repo, pr))
	}
	if !label(pr, "lgtm") {
		if _, _, err = c.Issues.AddLabelsToIssue(ctx, owner, repo, number, []string{"lgtm"}); err != nil {
			return err
		}
	}
	if err := gates(ctx, c, owner, repo, pr, controller); err != nil {
		return err
	}
	current, _, err := c.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	latest, err := authorize(ctx, c, cache, owner, repo, current, controller)
	if errors.Is(err, review.ErrWaiting) {
		return nil
	}
	if err != nil || latest == nil || latest.GetID() != approval.GetID() || current.GetHead().GetSHA() != pr.GetHead().GetSHA() {
		return errors.Join(err, withdraw(ctx, c, owner, repo, current))
	}
	if err := gates(ctx, c, owner, repo, current, controller); err != nil {
		return err
	}
	// Re-read approval and mutable PR flags after the slower protection/CI reads.
	final, _, err := c.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	finalApproval, err := authorize(ctx, c, cache, owner, repo, final, controller)
	if errors.Is(err, review.ErrWaiting) {
		return nil
	}
	if err != nil || finalApproval == nil || finalApproval.GetID() != approval.GetID() || final.GetHead().GetSHA() != pr.GetHead().GetSHA() {
		return errors.Join(err, withdraw(ctx, c, owner, repo, final))
	}
	if final.GetDraft() || label(final, "hold") || !label(final, "agent-managed") || final.GetState() != "open" || !final.GetMergeable() || final.GetMergeableState() != "clean" {
		return errors.New("PR changed before merge")
	}
	result, _, mergeErr := c.PullRequests.Merge(ctx, owner, repo, number, "", &github.PullRequestOptions{SHA: pr.GetHead().GetSHA(), MergeMethod: "merge"})
	if mergeErr == nil && result.GetMerged() {
		return nil
	}
	// A lost response does not justify a second merge request. The next poll
	// also reads PR state before attempting any mutation.
	after, _, readErr := c.PullRequests.Get(ctx, owner, repo, number)
	if readErr == nil && after.GetMerged() {
		return nil
	}
	if mergeErr == nil {
		mergeErr = errors.New("GitHub did not merge the PR")
	}
	return errors.Join(mergeErr, readErr)
}

func withdraw(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest) error {
	if !label(pr, "lgtm") {
		return nil
	}
	_, err := c.Issues.RemoveLabelForIssue(ctx, owner, repo, pr.GetNumber(), "lgtm")
	return err
}

const evidenceMarker = "<!-- kelos-merge/v1 -->\n"

type approvalRecord struct {
	ApprovalID  int64  `json:"approval_id"`
	SHA         string `json:"sha"`
	Version     string `json:"version"`
	Invalidated bool   `json:"invalidated"`
}

// Revocation survives restart and even a later return to the same head.
// A new comment is required after editing, deletion, feedback or observed pushes.
func authorize(ctx context.Context, c *github.Client, cache *review.Cache, owner, repo string, pr *github.PullRequest, controller string) (*github.IssueComment, error) {
	user, _, err := c.Users.Get(ctx, controller)
	if err != nil {
		return nil, err
	}
	if user.GetID() <= 0 {
		return nil, errors.New("controller identity unavailable")
	}
	comments := []*github.IssueComment{}
	options := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, res, err := c.Issues.ListComments(ctx, owner, repo, pr.GetNumber(), options)
		if err != nil {
			return nil, err
		}
		comments = append(comments, page...)
		if res.NextPage == 0 {
			break
		}
		options.Page = res.NextPage
	}
	records := map[int64]approvalRecord{}
	recordComments := map[int64]*github.IssueComment{}
	sources := map[int64]*github.IssueComment{}
	for _, comment := range comments {
		if comment.GetUser().GetLogin() == "geoberle" {
			if comment.GetID() <= 0 {
				return nil, errors.New("human comment identity unavailable")
			}
			sources[comment.GetID()] = comment
		}
		if comment.GetUser().GetID() != user.GetID() || !strings.HasPrefix(comment.GetBody(), evidenceMarker) {
			continue
		}
		var record approvalRecord
		if json.Unmarshal([]byte(strings.TrimPrefix(comment.GetBody(), evidenceMarker)), &record) != nil || record.ApprovalID <= 0 || len(record.SHA) != 40 || len(record.Version) == 0 {
			return nil, errors.New("malformed approval record")
		}
		if old, exists := records[record.ApprovalID]; exists {
			if old.SHA != record.SHA || old.Version != record.Version {
				return nil, errors.New("conflicting approval records")
			}
			if old.Invalidated {
				continue
			}
		}
		records[record.ApprovalID] = record
		recordComments[record.ApprovalID] = comment
	}
	// Add every observed syntactically valid approval, including stale ones, so
	// stale authorization cannot become valid when a branch returns to that SHA.
	for id, comment := range sources {
		if !isApproval(comment.GetBody()) {
			continue
		}
		if _, exists := records[id]; !exists {
			records[id] = approvalRecord{ApprovalID: id, SHA: pr.GetHead().GetSHA(), Version: comment.GetUpdatedAt().Format(time.RFC3339Nano)}
		}
	}
	var selected *github.IssueComment
	for id, record := range records {
		source := sources[id]
		valid := !record.Invalidated && source != nil && record.SHA == pr.GetHead().GetSHA() && isApproval(source.GetBody()) && !source.GetCreatedAt().IsZero() && source.GetCreatedAt().Equal(source.GetUpdatedAt()) && record.Version == source.GetUpdatedAt().Format(time.RFC3339Nano)
		if valid {
			for otherID, other := range sources {
				if otherID != id && (other.GetUpdatedAt().IsZero() || !other.GetUpdatedAt().Before(source.GetCreatedAt().Time)) {
					valid = false
					break
				}
			}
		}
		if valid {
			for _, comment := range comments {
				if comment.GetUser().GetID() == user.GetID() && strings.HasPrefix(comment.GetBody(), "<!-- kelos-feedback/v1 -->") && (comment.GetCreatedAt().IsZero() || !comment.GetCreatedAt().Before(source.GetCreatedAt().Time)) {
					valid = false
					break
				}
			}
		}
		if valid {
			blocked, err := laterReviewOrPush(ctx, c, owner, repo, pr.GetNumber(), source.GetCreatedAt().Time)
			if err != nil {
				return nil, err
			}
			valid = !blocked
		}
		if valid {
			// The command names no commit, so the head must demonstrably
			// have been the head when it was written; a push after the
			// approval is not approved.
			since, err := headSince(ctx, c, owner, repo, pr, source.GetCreatedAt().Time)
			if err != nil {
				return nil, err
			}
			valid = since
		}
		record.Invalidated = !valid
		encoded, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		body := evidenceMarker + string(encoded)
		var written *github.IssueComment
		if existing := recordComments[id]; existing == nil {
			written, _, err = c.Issues.CreateComment(ctx, owner, repo, pr.GetNumber(), &github.IssueComment{Body: &body})
		} else if existing.GetBody() != body {
			written, _, err = c.Issues.EditComment(ctx, owner, repo, existing.GetID(), &github.IssueComment{Body: &body})
		}
		if err != nil {
			return nil, err
		}
		if written != nil && written.GetUser().GetID() != user.GetID() {
			return nil, errors.New("credential does not match controller identity")
		}
		if valid && (selected == nil || source.GetCreatedAt().After(selected.GetCreatedAt().Time)) {
			selected = source
		}
	}
	if selected == nil {
		return automaticApproval(ctx, c, cache, owner, repo, pr, controller, comments)
	}
	permission, _, err := c.Repositories.GetPermissionLevel(ctx, owner, repo, "geoberle")
	if err != nil {
		return nil, err
	}
	if permission.GetRoleName() != "admin" && permission.GetRoleName() != "maintain" {
		return nil, errors.New("approval actor lacks admin/maintain role")
	}
	return selected, nil
}

// isApproval reports whether a comment is the human approval command. It
// names no commit: the controller binds it to the PR head it first observes
// and rejects it when anything was pushed after the comment was created.
func isApproval(body string) bool {
	return feedback.Approves(body)
}

// headSince reports whether the PR head has been the tip of its branch since
// before the given time. GitHub's push log for the branch lists every update
// with its time; the newest entry must have put the head there, and earlier
// than at. A branch in another repository has no log here and is refused.
func headSince(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest, at time.Time) (bool, error) {
	if pr.GetHead().GetRepo().GetFullName() != owner+"/"+repo || len(pr.GetHead().GetRef()) == 0 {
		return false, nil
	}
	// Newest first is the default order.
	activities, _, err := c.Repositories.ListRepositoryActivities(ctx, owner, repo, &github.ListRepositoryActivityOptions{Ref: "refs/heads/" + pr.GetHead().GetRef(), PerPage: 1})
	if err != nil {
		return false, err
	}
	if len(activities) == 0 {
		return false, nil
	}
	newest := activities[0]
	return newest.After == pr.GetHead().GetSHA() && !newest.Timestamp.IsZero() && newest.Timestamp.Before(at), nil
}

func laterReviewOrPush(ctx context.Context, c *github.Client, owner, repo string, number int, approved time.Time) (bool, error) {
	ro := &github.ListOptions{PerPage: 100}
	for {
		list, res, err := c.PullRequests.ListReviews(ctx, owner, repo, number, ro)
		if err != nil {
			return false, err
		}
		for _, review := range list {
			if review.GetUser().GetLogin() == "geoberle" && (len(review.GetBody()) > 0 || review.GetState() == "CHANGES_REQUESTED") && (review.GetSubmittedAt().IsZero() || !review.GetSubmittedAt().Before(approved)) {
				return true, nil
			}
		}
		if res.NextPage == 0 {
			break
		}
		ro.Page = res.NextPage
	}
	co := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		list, res, err := c.PullRequests.ListComments(ctx, owner, repo, number, co)
		if err != nil {
			return false, err
		}
		for _, comment := range list {
			if comment.GetUser().GetLogin() == "geoberle" && (comment.GetUpdatedAt().IsZero() || !comment.GetUpdatedAt().Before(approved)) {
				return true, nil
			}
		}
		if res.NextPage == 0 {
			break
		}
		co.Page = res.NextPage
	}
	to := &github.ListOptions{PerPage: 100}
	for {
		list, res, err := c.Issues.ListIssueTimeline(ctx, owner, repo, number, to)
		if err != nil {
			return false, err
		}
		for _, event := range list {
			if (event.GetEvent() == "head_ref_force_pushed" || event.GetEvent() == "head_ref_deleted" || event.GetEvent() == "head_ref_restored") && (event.GetCreatedAt().IsZero() || !event.GetCreatedAt().Before(approved)) {
				return true, nil
			}
		}
		if res.NextPage == 0 {
			break
		}
		to.Page = res.NextPage
	}
	return false, nil
}
