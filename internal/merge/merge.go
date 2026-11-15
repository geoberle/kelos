// Package merge reconciles commit-bound human merge authorization.
package merge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-github/v80/github"
)

// Poll considers only enrolled open PRs in explicitly configured repositories.
// Controller must be a dedicated identity, distinct from the authorized human.
func Poll(ctx context.Context, c *github.Client, repositories []string, controller string) error {
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
				if err := reconcile(ctx, c, owner, repo, pr.GetNumber(), controller); err != nil {
					failures = append(failures, fmt.Errorf("%s#%d: %w", repository, pr.GetNumber(), err))
				}
			}
			if response.NextPage == 0 {
				break
			}
			opts.Page = response.NextPage
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

func reconcile(ctx context.Context, c *github.Client, owner, repo string, number int, controller string) error {
	pr, _, err := c.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	if pr.GetMerged() {
		return nil
	}
	comments := []*github.IssueComment{}
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		items, r, e := c.Issues.ListComments(ctx, owner, repo, number, opts)
		if e != nil {
			return e
		}
		comments = append(comments, items...)
		if r.NextPage == 0 {
			break
		}
		opts.Page = r.NextPage
	}
	var approval *github.IssueComment
	for _, comment := range comments {
		if comment.User.GetLogin() == "geoberle" && comment.GetBody() == "lgtm "+pr.Head.GetSHA() && len(pr.Head.GetSHA()) == 40 && comment.GetCreatedAt().Equal(comment.GetUpdatedAt()) {
			if approval == nil || comment.GetCreatedAt().After(approval.GetCreatedAt().Time) {
				approval = comment
			}
		}
	}
	if approval != nil {
		for _, comment := range comments {
			if comment.User.GetLogin() == "geoberle" && comment.GetID() != approval.GetID() && !strings.HasPrefix(comment.GetBody(), "lgtm ") && !comment.GetUpdatedAt().Before(approval.GetCreatedAt().Time) {
				approval = nil
				break
			}
		}
	}
	if approval == nil {
		if label(pr, "lgtm") {
			_, err = c.Issues.RemoveLabelForIssue(ctx, owner, repo, number, "lgtm")
		}
		return err
	}
	permission, _, err := c.Repositories.GetPermissionLevel(ctx, owner, repo, "geoberle")
	if err != nil {
		return err
	}
	if permission.GetRoleName() != "admin" && permission.GetRoleName() != "maintain" {
		return errors.New("human role is not admin or maintain")
	}
	// Durable evidence is owned by the controller; a human-written copy is ignored.
	evidence := fmt.Sprintf("<!-- kelos-merge-v1 -->\napproval=%d actor=geoberle sha=%s version=%s", approval.GetID(), pr.Head.GetSHA(), approval.GetUpdatedAt().Format("2006-01-02T15:04:05Z"))
	found := false
	for _, comment := range comments {
		if comment.User.GetLogin() == controller && comment.GetBody() == evidence {
			found = true
		}
	}
	if !found {
		_, _, err = c.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{Body: &evidence})
		return err
	}
	if !label(pr, "lgtm") {
		_, _, err = c.Issues.AddLabelsToIssue(ctx, owner, repo, number, []string{"lgtm"})
		return err
	}
	return errors.New("merge disabled: complete final gate validation is not yet implemented")
}
