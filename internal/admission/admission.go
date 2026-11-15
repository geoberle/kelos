package admission

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-github/v80/github"
)

// Poll reconciles admission labels only in the explicit repository allowlist.
// It never creates or deletes Kelos Tasks.
func Poll(ctx context.Context, client *github.Client, repositories []string) error {
	var failures []error
	for _, repository := range repositories {
		owner, repo, _ := strings.Cut(repository, "/")
		if err := pollRepository(ctx, client, owner, repo); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", repository, err))
		}
	}
	return errors.Join(failures...)
}

func pollRepository(ctx context.Context, c *github.Client, owner, repo string) error {
	opts := &github.IssueListByRepoOptions{State: "all", ListOptions: github.ListOptions{PerPage: 100}}
	for {
		issues, response, err := c.Issues.ListByRepo(ctx, owner, repo, opts)
		if err != nil {
			return err
		}
		for _, issue := range issues {
			if issue.IsPullRequest() {
				continue
			}
			ready, started := false, false
			for _, label := range issue.Labels {
				switch label.GetName() {
				case "ready-for-agent":
					ready = true
				case "agent-start":
					started = true
				}
			}
			eligible := ready && issue.GetState() == "open"
			if eligible {
				options := &github.ListOptions{PerPage: 100}
				for {
					request, err := c.NewRequest("GET", fmt.Sprintf("repos/%s/%s/issues/%d/dependencies/blocked_by?per_page=100&page=%d", owner, repo, issue.GetNumber(), options.Page), nil)
					if err != nil {
						return err
					}
					var blockers []*github.Issue
					response, err := c.Do(ctx, request, &blockers)
					if err != nil {
						return err
					}
					for _, blocker := range blockers {
						if blocker.GetState() != "closed" {
							eligible = false
						}
					}
					if response.NextPage == 0 {
						break
					}
					options.Page = response.NextPage
				}
			}
			if eligible && !started {
				if _, _, err := c.Issues.AddLabelsToIssue(ctx, owner, repo, issue.GetNumber(), []string{"agent-start"}); err != nil {
					return err
				}
			}
			if !eligible && started {
				if _, err := c.Issues.RemoveLabelForIssue(ctx, owner, repo, issue.GetNumber(), "agent-start"); err != nil {
					return err
				}
			}
		}
		if response.NextPage == 0 {
			return nil
		}
		opts.ListOptions.Page = response.NextPage
	}
}
