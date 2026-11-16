package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"

	"github.com/google/go-github/v80/github"
)

// gates reads the effective branch rules, which include organization rulesets.
func gates(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest) error {
	if pr.GetState() != "open" || pr.GetDraft() || pr.GetMerged() || !label(pr, "agent-managed") || label(pr, "hold") || !pr.GetMergeable() || pr.GetMergeableState() != "clean" {
		return fmt.Errorf("PR is not ready to merge")
	}
	if err := requiredCI(ctx, c, owner, repo, pr); err != nil {
		return err
	}
	return resolvedThreads(ctx, c, owner, repo, pr.GetNumber())
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
			AllowedMergeMethods []string `json:"allowed_merge_methods"`
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
	for _, rule := range rules {
		switch rule.Type {
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
	// Every check that ran must be green; none is named in advance. A skipped
	// or neutral check ran nothing that could fail.
	for name, apps := range checks {
		for _, check := range apps {
			if check.GetStatus() != "completed" {
				return fmt.Errorf("check %s is not complete", name)
			}
			switch check.GetConclusion() {
			case "success", "skipped", "neutral":
			default:
				return fmt.Errorf("check %s is not successful", name)
			}
		}
	}
	for name, status := range statuses {
		if status.GetState() != "success" {
			return fmt.Errorf("status %s is not successful", name)
		}
	}
	return nil
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
