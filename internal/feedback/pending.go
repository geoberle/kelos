package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/go-github/v80/github"
)

const handledMarker = "<!-- kelos-handled-feedback/v1 -->\n"

type receipt struct {
	Task   string   `json:"task"`
	Commit string   `json:"commit"`
	IDs    []string `json:"ids"`
}

const observedMarker = "<!-- kelos-feedback/v1 -->\n"

// Pending preserves observed requests even if their original comments disappear.
// A controller-owned observation is task input, never merge authorization.
func Pending(ctx context.Context, c *github.Client, owner, repo string, number int, head, controller string) ([]Finding, error) {
	if len(controller) == 0 || controller == "geoberle" {
		return nil, fmt.Errorf("dedicated feedback controller required")
	}
	current, err := List(ctx, c, owner, repo, number)
	if err != nil {
		return nil, err
	}
	pending := []Finding{}
	seen := map[string]bool{}
	var receipts []receipt
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, res, err := c.Issues.ListComments(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, err
		}
		for _, comment := range page {
			if comment.GetUser().GetLogin() == controller && strings.HasPrefix(comment.GetBody(), handledMarker) {
				var r receipt
				if err := json.Unmarshal([]byte(strings.TrimPrefix(comment.GetBody(), handledMarker)), &r); err != nil {
					return nil, err
				}
				if len(r.Task) == 0 || len(r.Commit) == 0 || len(r.IDs) == 0 {
					return nil, fmt.Errorf("invalid handled feedback receipt")
				}
				receipts = append(receipts, r)
			}

			if comment.GetUser().GetLogin() != controller || !strings.HasPrefix(comment.GetBody(), observedMarker) {
				continue
			}
			var findings []Finding
			if err := json.Unmarshal([]byte(strings.TrimPrefix(comment.GetBody(), observedMarker)), &findings); err != nil {
				return nil, fmt.Errorf("invalid retained feedback: %w", err)
			}
			for _, f := range findings {
				if !strings.HasPrefix(f.ID, "human/") || len(strings.TrimSpace(f.Content)) == 0 {
					return nil, fmt.Errorf("invalid retained feedback identity")
				}
				if !seen[f.ID] {
					pending = append(pending, f)
					seen[f.ID] = true
				}
			}
		}
		if res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}
	fresh := []Finding{}
	for _, f := range current {
		if !seen[f.ID] {
			fresh = append(fresh, f)
			pending = append(pending, f)
			seen[f.ID] = true
		}
	}
	if len(fresh) > 0 {
		data, err := json.Marshal(fresh)
		if err != nil {
			return nil, err
		}
		body := observedMarker + string(data)
		if len(body) > 60000 {
			return nil, fmt.Errorf("feedback exceeds durable comment capacity")
		}
		written, _, err := c.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{Body: &body})
		if err != nil {
			return nil, err
		}
		if written.GetUser().GetLogin() != controller {
			return nil, fmt.Errorf("feedback credential does not match controller")
		}
	}
	handled := map[string]bool{}
	for _, r := range receipts {
		if r.Commit != head {
			comparison, _, err := c.Repositories.CompareCommits(ctx, owner, repo, r.Commit, head, nil)
			if err != nil {
				return nil, err
			}
			if comparison.GetStatus() != "ahead" && comparison.GetStatus() != "identical" {
				continue
			}
		}
		for _, id := range r.IDs {
			handled[id] = true
		}
	}
	unhandled := pending[:0]
	for _, f := range pending {
		if !handled[f.ID] {
			unhandled = append(unhandled, f)
		}
	}
	return unhandled, nil
}

// Handled returns the finding IDs that an editor addressed at exactly head,
// read from the controller's retained receipts. A review finding ID names the
// head it was raised on, so a receipt for any other commit cannot match one.
func Handled(ctx context.Context, c *github.Client, owner, repo string, number int, head, controller string) (map[string]bool, error) {
	handled := map[string]bool{}
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, res, err := c.Issues.ListComments(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, err
		}
		for _, comment := range page {
			if comment.GetUser().GetLogin() != controller || !strings.HasPrefix(comment.GetBody(), handledMarker) {
				continue
			}
			var r receipt
			if err := json.Unmarshal([]byte(strings.TrimPrefix(comment.GetBody(), handledMarker)), &r); err != nil {
				return nil, err
			}
			if r.Commit != head {
				continue
			}
			for _, id := range r.IDs {
				handled[id] = true
			}
		}
		if res.NextPage == 0 {
			return handled, nil
		}
		opts.Page = res.NextPage
	}
}

// Record is called only after the editor result, commit ancestry and replies have
// been validated. Retained receipts allow review completion to survive restarts.
func Record(ctx context.Context, c *github.Client, owner, repo string, number int, commit, controller, task string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	data, err := json.Marshal(receipt{Task: task, Commit: commit, IDs: ids})
	if err != nil {
		return err
	}
	body := handledMarker + string(data)
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, res, err := c.Issues.ListComments(ctx, owner, repo, number, opts)
		if err != nil {
			return err
		}
		for _, comment := range page {
			if comment.GetUser().GetLogin() == controller && comment.GetBody() == body {
				return nil
			}
		}
		if res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}
	written, _, err := c.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{Body: &body})
	if err != nil {
		return err
	}
	if written.GetUser().GetLogin() != controller {
		return fmt.Errorf("feedback credential does not match controller")
	}
	return nil
}
