package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/google/go-github/v80/github"
)

// gates reads the effective branch rules, which include organization rulesets.
// The verify result comes last, so that verify only runs for a pull request
// nothing else holds back.
func gates(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest, controller string) error {
	if pr.GetState() != "open" || pr.GetDraft() || pr.GetMerged() || !label(pr, "agent-managed") || label(pr, "hold") || !pr.GetMergeable() {
		return fmt.Errorf("PR is not ready to merge")
	}
	if err := requiredCI(ctx, c, owner, repo, pr); err != nil {
		var missing *missingCheck
		if errors.As(err, &missing) {
			return errors.Join(err, reportBlocked(ctx, c, owner, repo, pr, controller, missing))
		}
		return err
	}
	if err := resolvedThreads(ctx, c, owner, repo, pr.GetNumber()); err != nil {
		return err
	}
	if err := verified(ctx, c, owner, repo, pr, controller); err != nil {
		return err
	}
	// A verify result that is pending or missing keeps the state from being clean.
	if pr.GetMergeableState() != "clean" {
		return fmt.Errorf("PR is not ready to merge")
	}
	return nil
}

// requiredCI checks what GitHub's rules do not express for this controller:
// that the target only takes pull requests and still permits merge commits,
// and that every check that ran on the commit is green. GitHub enforces the
// rules themselves when the merge is requested.
func requiredCI(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest) error {
	branch := pr.GetBase().GetRef()
	if len(branch) == 0 {
		return fmt.Errorf("missing target branch")
	}
	// Effective rules include those of organization rulesets.
	request, err := c.NewRequest("GET", fmt.Sprintf("repos/%s/%s/rules/branches/%s?per_page=100", owner, repo, url.PathEscape(branch)), nil)
	if err != nil {
		return err
	}
	var rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			AllowedMergeMethods  []string `json:"allowed_merge_methods"`
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	response, err := c.Do(ctx, request, &rules)
	if err != nil {
		return fmt.Errorf("effective rules unavailable: %w", err)
	}
	if response.NextPage != 0 {
		return fmt.Errorf("effective rules exceed one page")
	}
	pullRequestRequired := false
	var required []string
	for _, rule := range rules {
		switch rule.Type {
		case "required_status_checks":
			for _, check := range rule.Parameters.RequiredStatusChecks {
				required = append(required, check.Context)
			}
		case "pull_request":
			pullRequestRequired = true
			if len(rule.Parameters.AllowedMergeMethods) > 0 && !slices.Contains(rule.Parameters.AllowedMergeMethods, "merge") {
				return fmt.Errorf("target prohibits merge commits")
			}
		case "required_linear_history":
			return fmt.Errorf("target prohibits merge commits")
		}
	}
	if !pullRequestRequired {
		return fmt.Errorf("target branch does not require pull requests")
	}
	checkSHA := pr.GetMergeCommitSHA()
	if len(checkSHA) == 0 {
		checkSHA = pr.GetHead().GetSHA()
	}
	checks, statuses, err := commitChecks(ctx, c, owner, repo, checkSHA)
	if err != nil {
		return err
	}
	// GitHub prefers test-merge results when present; otherwise it uses head.
	if checkSHA != pr.GetHead().GetSHA() && len(checks)+len(statuses) == 0 {
		checks, statuses, err = commitChecks(ctx, c, owner, repo, pr.GetHead().GetSHA())
		if err != nil {
			return err
		}
	}
	// GitHub refuses the merge while a required check is absent and only
	// reports the pull request as blocked. Naming the check says what to do.
	for _, name := range required {
		if _, ran := checks[name]; ran {
			continue
		}
		if _, reported := statuses[name]; reported {
			continue
		}
		return &missingCheck{name: name, head: pr.GetHead().GetSHA(), base: branch}
	}
	// Every check that ran must be green; none is named in advance. A skipped
	// or neutral check ran nothing that could fail.
	for name, apps := range checks {
		for _, check := range apps {
			if check.GetStatus() != "completed" {
				return fmt.Errorf("check %s is not complete: %w", name, errChecking)
			}
			switch check.GetConclusion() {
			case "success", "skipped", "neutral":
			default:
				return fmt.Errorf("check %s is not successful", name)
			}
		}
	}
	for name, status := range statuses {
		// The verify result is judged against the current base separately.
		if name != verifyContext && status.GetState() != "success" {
			return fmt.Errorf("status %s is not successful", name)
		}
	}
	return nil
}

// errChecking reports that a check on the commit is still running. The
// controller waits for it; that is not a failure.
var errChecking = errors.New("waiting for the check to finish")

// missingCheck is a check the target branch requires that has not run on the
// pull request's commit.
type missingCheck struct {
	name, head, base string
}

func (m *missingCheck) Error() string {
	return fmt.Sprintf("required check %s has not run on %s", m.name, m.head)
}

const blockedMarker = "<!-- kelos-merge-blocked/v1 "

// reportBlocked comments once per head that an approved pull request cannot
// merge because a required check has not run on it.
func reportBlocked(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest, controller string, missing *missingCheck) error {
	marker := blockedMarker + missing.head + " -->"
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, response, err := c.Issues.ListComments(ctx, owner, repo, pr.GetNumber(), opts)
		if err != nil {
			return err
		}
		for _, comment := range comments {
			if comment.GetUser().GetLogin() == controller && strings.HasPrefix(comment.GetBody(), marker) {
				return nil
			}
		}
		if response.NextPage == 0 {
			break
		}
		opts.Page = response.NextPage
	}
	body := marker + "\nThis pull request is approved but cannot merge: the required check `" + missing.name + "` has not run on commit " + missing.head + ". The branch probably predates the workflow that reports it. Rebase it onto `" + missing.base + "`; commenting `/kelos run` on its issue lets the agent do that."
	_, _, err := c.Issues.CreateComment(ctx, owner, repo, pr.GetNumber(), &github.IssueComment{Body: &body})
	return err
}

func resolvedThreads(ctx context.Context, c *github.Client, owner, repo string, number int) error {
	cursor := ""
	for {
		variables := map[string]any{"owner": owner, "repo": repo, "number": number, "cursor": nil}
		if len(cursor) > 0 {
			variables["cursor"] = cursor
		}
		payload := map[string]any{"query": `query($owner:String!,$repo:String!,$number:Int!,$cursor:String){repository(owner:$owner,name:$repo){pullRequest(number:$number){reviewThreads(first:100,after:$cursor){nodes{isResolved} pageInfo{hasNextPage endCursor}}}}}`, "variables": variables}
		request, err := c.NewRequest("POST", "graphql", payload)
		if err != nil {
			return err
		}
		var result struct {
			Errors []json.RawMessage `json:"errors"`
			Data   struct {
				Repository struct {
					PullRequest struct {
						ReviewThreads *struct {
							Nodes []struct {
								IsResolved *bool `json:"isResolved"`
							}
							PageInfo struct {
								HasNextPage *bool
								EndCursor   string
							}
						}
					}
				}
			}
		}
		if _, err = c.Do(ctx, request, &result); err != nil {
			return err
		}
		threads := result.Data.Repository.PullRequest.ReviewThreads
		if len(result.Errors) > 0 || threads == nil || threads.PageInfo.HasNextPage == nil {
			return fmt.Errorf("review thread evidence unavailable")
		}
		for _, thread := range threads.Nodes {
			if thread.IsResolved == nil || !*thread.IsResolved {
				return fmt.Errorf("unresolved review thread")
			}
		}
		if !*threads.PageInfo.HasNextPage {
			return nil
		}
		next := threads.PageInfo.EndCursor
		if len(next) == 0 || next == cursor {
			return fmt.Errorf("invalid thread pagination")
		}
		cursor = next
	}
}

func commitChecks(ctx context.Context, c *github.Client, owner, repo, sha string) (map[string]map[int64]*github.CheckRun, map[string]*github.RepoStatus, error) {
	checks := map[string]map[int64]*github.CheckRun{}
	opts := &github.ListCheckRunsOptions{Filter: github.Ptr("latest"), ListOptions: github.ListOptions{PerPage: 100}}
	for {
		result, res, err := c.Checks.ListCheckRunsForRef(ctx, owner, repo, sha, opts)
		if err != nil {
			return nil, nil, err
		}
		for _, check := range result.CheckRuns {
			if check.GetHeadSHA() != sha {
				return nil, nil, fmt.Errorf("check does not match requested commit")
			}
			name, app := check.GetName(), check.GetApp().GetID()
			if checks[name] == nil {
				checks[name] = map[int64]*github.CheckRun{}
			}
			if old := checks[name][app]; old == nil || check.GetID() > old.GetID() {
				checks[name][app] = check
			}
		}
		if res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}
	statuses := map[string]*github.RepoStatus{}
	so := &github.ListOptions{PerPage: 100}
	for {
		list, res, err := c.Repositories.ListStatuses(ctx, owner, repo, sha, so)
		if err != nil {
			return nil, nil, err
		}
		// GitHub returns statuses newest first, including across pages.
		for _, status := range list {
			if statuses[status.GetContext()] == nil {
				statuses[status.GetContext()] = status
			}
		}
		if res.NextPage == 0 {
			break
		}
		so.Page = res.NextPage
	}

	return checks, statuses, nil
}
