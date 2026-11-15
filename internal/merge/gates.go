package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/google/go-github/v80/github"
)

// gates supports classic branch protection. Unknown policy is never permission.
func gates(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest) error {
	if pr.GetState() != "open" || pr.GetDraft() || pr.GetMerged() || !label(pr, "agent-managed") || label(pr, "hold") || !pr.GetMergeable() || pr.GetMergeableState() != "clean" {
		return fmt.Errorf("PR is not ready to merge")
	}
	branch := pr.GetBase().GetRef()
	if len(branch) == 0 {
		return fmt.Errorf("missing target branch")
	}
	protection, _, err := c.Repositories.GetBranchProtection(ctx, owner, repo, branch)
	if err != nil {
		return fmt.Errorf("branch enforcement unavailable: %w", err)
	}
	if protection == nil || protection.EnforceAdmins == nil || !protection.EnforceAdmins.Enabled || protection.RequiredConversationResolution == nil || !protection.RequiredConversationResolution.Enabled || protection.RequiredStatusChecks == nil || !protection.RequiredStatusChecks.Strict {
		return fmt.Errorf("required enforcement is not configured")
	}
	if protection.RequireLinearHistory != nil && protection.RequireLinearHistory.Enabled {
		return fmt.Errorf("target prohibits merge commits")
	}
	if reviews := protection.RequiredPullRequestReviews; reviews != nil && reviews.BypassPullRequestAllowances != nil {
		bypass := reviews.BypassPullRequestAllowances
		if len(bypass.Users)+len(bypass.Teams)+len(bypass.Apps) > 0 {
			return fmt.Errorf("pull request bypass is configured")
		}
	}
	// Preserve unknown rule types so a newly introduced rule cannot disappear
	// through typed decoding. Rulesets need their own non-bypass verification.
	request, err := c.NewRequest("GET", fmt.Sprintf("repos/%s/%s/rules/branches/%s?per_page=100", owner, repo, url.PathEscape(branch)), nil)
	if err != nil {
		return err
	}
	var rules []json.RawMessage
	response, err := c.Do(ctx, request, &rules)
	if err != nil {
		return fmt.Errorf("effective rules unavailable: %w", err)
	}
	if rules == nil || len(rules) > 0 || response.NextPage != 0 {
		return fmt.Errorf("ruleset enforcement is not supported")
	}
	required := []*github.RequiredStatusCheck{}
	if protection.RequiredStatusChecks.Checks != nil {
		required = append(required, (*protection.RequiredStatusChecks.Checks)...)
	}
	if protection.RequiredStatusChecks.Contexts != nil {
		for _, name := range *protection.RequiredStatusChecks.Contexts {
			found := false
			for _, check := range required {
				if check != nil && check.Context == name {
					found = true
				}
			}
			if !found {
				required = append(required, &github.RequiredStatusCheck{Context: name})
			}
		}
	}
	if len(required) == 0 {
		return fmt.Errorf("no required CI checks configured")
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
	for _, requiredCheck := range required {
		if requiredCheck == nil || len(requiredCheck.Context) == 0 {
			return fmt.Errorf("malformed required check")
		}
		name := requiredCheck.Context
		found := false
		for app, check := range checks[name] {
			if requiredCheck.GetAppID() > 0 && app != requiredCheck.GetAppID() {
				continue
			}
			found = true
			if check.GetStatus() != "completed" || check.GetConclusion() != "success" {
				return fmt.Errorf("required check %s is not successful", name)
			}
		}
		if status := statuses[name]; status != nil {
			// A legacy commit status cannot prove a pinned GitHub App identity.
			if requiredCheck.GetAppID() <= 0 {
				found = true
			}
			if status.GetState() != "success" {
				return fmt.Errorf("required status %s is not successful", name)
			}
		}
		if !found {
			return fmt.Errorf("required check %s is missing", name)
		}
	}
	return resolvedThreads(ctx, c, owner, repo, pr.GetNumber())
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
