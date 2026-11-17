// Package feedback retains human requests and verified editor dispositions.
package feedback

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/google/go-github/v80/github"
)

type Finding struct {
	ID              string `json:"id"`
	Content         string `json:"content"`
	Path            string `json:"path"`
	Source          string `json:"source"`
	ReviewCommentID int64  `json:"review_comment_id,omitempty"`
}

func List(ctx context.Context, c *github.Client, owner, repo string, number int) ([]Finding, error) {
	var findings []Finding
	add := func(kind string, id int64, body, source, version, path string, reply int64) {
		if len(strings.TrimSpace(body)) == 0 {
			return
		}
		// A comment that carries a control command is not a repair request,
		// whatever else it says.
		if HasCommand(body, ApproveCommand) || HasCommand(body, ReassessCommand) {
			return
		}
		hash := sha256.Sum256([]byte(version + "\n" + body))
		findings = append(findings, Finding{ID: fmt.Sprintf("human/%s/%d/%x", kind, id, hash), Content: body, Source: source, Path: path, ReviewCommentID: reply})
	}
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, res, err := c.Issues.ListComments(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, err
		}
		for _, v := range page {
			if v.GetUser().GetLogin() == "geoberle" {
				add("comment", v.GetID(), v.GetBody(), v.GetHTMLURL(), v.GetUpdatedAt().String(), "", 0)
			}
		}
		if res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}
	ro := &github.ListOptions{PerPage: 100}
	for {
		page, res, err := c.PullRequests.ListReviews(ctx, owner, repo, number, ro)
		if err != nil {
			return nil, err
		}
		for _, v := range page {
			if v.GetUser().GetLogin() == "geoberle" {
				add("review", v.GetID(), v.GetBody(), v.GetHTMLURL(), v.GetSubmittedAt().String(), "", 0)
			}
		}
		if res.NextPage == 0 {
			break
		}
		ro.Page = res.NextPage
	}
	co := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, res, err := c.PullRequests.ListComments(ctx, owner, repo, number, co)
		if err != nil {
			return nil, err
		}
		for _, v := range page {
			if v.GetUser().GetLogin() == "geoberle" {
				reply := v.GetID()
				if v.GetInReplyTo() != 0 {
					reply = v.GetInReplyTo()
				}
				add("inline", v.GetID(), v.GetBody(), v.GetHTMLURL(), v.GetUpdatedAt().String(), v.GetPath(), reply)
			}
		}
		if res.NextPage == 0 {
			break
		}
		co.Page = res.NextPage
	}
	if len(findings) > 0 {
		permission, _, err := c.Repositories.GetPermissionLevel(ctx, owner, repo, "geoberle")
		if err != nil {
			return nil, err
		}
		if permission.GetRoleName() != "admin" && permission.GetRoleName() != "maintain" {
			return nil, nil
		}
	}
	return findings, nil
}
