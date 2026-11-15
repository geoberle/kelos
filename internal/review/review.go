// Package review consumes evidence from the trusted OCR workflow.
package review

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v80/github"
)

// Poll reconciles enrolled PRs in the explicit allowlist.
func Poll(ctx context.Context, c *github.Client, repositories []string) error {
	var failures []error
	for _, repository := range repositories {
		owner, repo, _ := strings.Cut(repository, "/")
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
				if err := reconcile(ctx, c, owner, repo, pr); err != nil {
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

type identity struct {
	Repository string `json:"repository"`
	PR         int    `json:"pr"`
	Run        int64  `json:"run"`
	Attempt    int    `json:"attempt"`
	Head       string `json:"head"`
	Base       string `json:"base"`
}
type item struct {
	ID   string `json:"item_id"`
	Path string `json:"path"`
}
type result struct {
	Comments []struct {
		Path     string `json:"path"`
		Start    int    `json:"start_line"`
		End      int    `json:"end_line"`
		Content  string `json:"content"`
		Severity string `json:"severity"`
	} `json:"comments"`
	Manifest struct {
		Schema    string          `json:"schema_version"`
		Operation string          `json:"operation"`
		State     string          `json:"terminal_state"`
		Failure   json.RawMessage `json:"run_failure"`
		Input     struct {
			Mode string `json:"mode"`
			Head string `json:"resolved_head"`
			Base string `json:"resolved_base"`
		} `json:"input"`
		Coverage struct{ Selected, Completed, Reused, Failed, Waived, Excluded []item } `json:"coverage"`
	} `json:"manifest"`
}

func reconcile(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest) error {
	reason, err := evaluate(ctx, c, owner, repo, pr)
	if err != nil {
		if label(pr, "review-done") {
			_, removeErr := c.Issues.RemoveLabelForIssue(ctx, owner, repo, pr.GetNumber(), "review-done")
			return errors.Join(err, removeErr)
		}
		return err
	}
	body := fmt.Sprintf("<!-- kelos-review/v1 -->\nHead: %s\n%s", pr.GetHead().GetSHA(), reason)
	// Use authenticated author identity, never trust a marker supplied by a PR author.
	user, _, err := c.Users.Get(ctx, "")
	if err != nil {
		return err
	}
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	var existing *github.IssueComment
	for {
		comments, response, err := c.Issues.ListComments(ctx, owner, repo, pr.GetNumber(), opts)
		if err != nil {
			return err
		}
		for _, comment := range comments {
			if comment.GetUser().GetID() == user.GetID() && strings.HasPrefix(comment.GetBody(), "<!-- kelos-review/v1 -->") {
				existing = comment
			}
			// Until feedback dispositions are delivered, preserve human input conservatively.
			if comment.GetUser().GetLogin() == "geoberle" {
				reason = "Human feedback requires disposition."
				body = fmt.Sprintf("<!-- kelos-review/v1 -->\nHead: %s\n%s", pr.GetHead().GetSHA(), reason)
			}
		}
		if response.NextPage == 0 {
			break
		}
		opts.Page = response.NextPage
	}
	reviewOptions := &github.ListOptions{PerPage: 100}
	for {
		reviews, response, e := c.PullRequests.ListReviews(ctx, owner, repo, pr.GetNumber(), reviewOptions)
		if e != nil {
			return e
		}
		for _, r := range reviews {
			if r.GetUser().GetLogin() == "geoberle" && r.GetState() != "APPROVED" {
				reason = "Human feedback requires disposition."
			}
		}
		if response.NextPage == 0 {
			break
		}
		reviewOptions.Page = response.NextPage
	}
	commentOptions := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, response, e := c.PullRequests.ListComments(ctx, owner, repo, pr.GetNumber(), commentOptions)
		if e != nil {
			return e
		}
		for _, r := range comments {
			if r.GetUser().GetLogin() == "geoberle" {
				reason = "Human feedback requires disposition."
			}
		}
		if response.NextPage == 0 {
			break
		}
		commentOptions.Page = response.NextPage
	}
	body = fmt.Sprintf("<!-- kelos-review/v1 -->\nHead: %s\n%s", pr.GetHead().GetSHA(), reason)
	if existing == nil {
		_, _, err = c.Issues.CreateComment(ctx, owner, repo, pr.GetNumber(), &github.IssueComment{Body: &body})
	} else if existing.GetBody() != body {
		_, _, err = c.Issues.EditComment(ctx, owner, repo, existing.GetID(), &github.IssueComment{Body: &body})
	}
	if err != nil {
		return err
	}
	if reason != "Complete current review; no must-fix findings." {
		if label(pr, "review-done") {
			_, err = c.Issues.RemoveLabelForIssue(ctx, owner, repo, pr.GetNumber(), "review-done")
			return err
		}
		return nil
	}
	current, _, err := c.PullRequests.Get(ctx, owner, repo, pr.GetNumber())
	if err != nil {
		return err
	}
	if current.GetHead().GetSHA() != pr.GetHead().GetSHA() {
		return nil
	}
	if label(current, "review-done") {
		return nil
	}
	_, _, err = c.Issues.AddLabelsToIssue(ctx, owner, repo, pr.GetNumber(), []string{"review-done"})
	return err
}

func evaluate(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest) (string, error) {
	workflow, _, err := c.Actions.GetWorkflowByFileName(ctx, owner, repo, "ocr.yml")
	if err != nil {
		return "", err
	}
	opts := &github.ListWorkflowRunsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		runs, response, err := c.Actions.ListWorkflowRunsByID(ctx, owner, repo, workflow.GetID(), opts)
		if err != nil {
			return "", err
		}
		for _, run := range runs.WorkflowRuns {
			if run.GetEvent() != "pull_request_target" || run.GetWorkflowID() != workflow.GetID() || run.GetPath() != ".github/workflows/ocr.yml" {
				continue
			}
			data, err := artifact(ctx, c, owner, repo, run.GetID(), fmt.Sprintf("kelos-ocr-identity-%d-%d", run.GetID(), run.GetRunAttempt()), "identity.json")
			if err != nil {
				return "Missing trusted correlation evidence; manual rerun required.", nil
			}
			var id identity
			if json.Unmarshal(data, &id) != nil || id.Repository != owner+"/"+repo || id.Run != run.GetID() || id.Attempt != run.GetRunAttempt() {
				return "Malformed correlation evidence; manual rerun required.", nil
			}
			if id.PR != pr.GetNumber() {
				continue
			}
			if id.Head != pr.GetHead().GetSHA() {
				return "Waiting for review of current head.", nil
			}
			if run.GetStatus() != "completed" || run.GetConclusion() != "success" {
				return "Review unfinished or publication failed; manual rerun required.", nil
			}
			raw, err := artifact(ctx, c, owner, repo, run.GetID(), fmt.Sprintf("ocr-review-result-%d-%d", run.GetID(), run.GetRunAttempt()), "ocr-result.json")
			if err != nil {
				return "Missing review artifact; manual rerun required.", nil
			}
			var r result
			if json.Unmarshal(raw, &r) != nil {
				return "Malformed review artifact; manual rerun required.", nil
			}
			m := r.Manifest
			if m.Schema != "ocr.run-manifest/v1" || m.Operation != "review" || m.Input.Mode != "range" || m.Input.Head != id.Head || len(id.Base) == 0 {
				return "Invalid review identity; manual rerun required.", nil
			}
			comparison, _, err := c.Repositories.CompareCommits(ctx, owner, repo, id.Base, id.Head, nil)
			if err != nil {
				return "", err
			}
			if m.Input.Base != comparison.GetMergeBaseCommit().GetSHA() {
				return "Reviewed range mismatch; manual rerun required.", nil
			}
			if m.State == "skipped" {
				return "Review skipped; human LGTM required.", nil
			}
			if m.State != "complete" || (len(m.Failure) > 0 && string(m.Failure) != "null") {
				return "Review incomplete; manual rerun required.", nil
			}
			cov := m.Coverage
			if cov.Selected == nil || cov.Completed == nil || cov.Reused == nil || cov.Failed == nil || cov.Waived == nil || cov.Excluded == nil || len(cov.Selected) == 0 || len(cov.Failed)+len(cov.Waived)+len(cov.Excluded) > 0 {
				return "Coverage incomplete or excluded; human attention required.", nil
			}
			currentRange, _, err := c.Repositories.CompareCommits(ctx, owner, repo, pr.GetBase().GetSHA(), id.Head, nil)
			if err != nil {
				return "", err
			}
			if currentRange.GetMergeBaseCommit().GetSHA() != m.Input.Base {
				return "Review range is stale.", nil
			}
			paths := map[string]bool{}
			for _, i := range cov.Selected {
				paths[i.Path] = true
			}
			fileOptions := &github.ListOptions{PerPage: 100}
			fileCount := 0
			for {
				files, response, err := c.PullRequests.ListFiles(ctx, owner, repo, pr.GetNumber(), fileOptions)
				if err != nil {
					return "", err
				}
				for _, file := range files {
					fileCount++
					if !paths[file.GetFilename()] {
						return "Changed file omitted from review coverage.", nil
					}
					delete(paths, file.GetFilename())
				}
				if response.NextPage == 0 {
					break
				}
				fileOptions.Page = response.NextPage
			}
			if len(paths) > 0 || fileCount >= 3000 {
				return "Coverage cannot be verified.", nil
			}
			selected := map[string]string{}
			for _, i := range cov.Selected {
				if len(i.ID) == 0 || len(i.Path) == 0 || len(selected[i.ID]) > 0 {
					return "Malformed coverage.", nil
				}
				selected[i.ID] = i.Path
			}
			for _, i := range append(cov.Completed, cov.Reused...) {
				if selected[i.ID] != i.Path || len(i.Path) == 0 {
					return "Malformed coverage.", nil
				}
				delete(selected, i.ID)
			}
			if len(selected) > 0 {
				return "Incomplete coverage.", nil
			}
			mustFix := false
			for _, finding := range r.Comments {
				if len(finding.Path) == 0 || len(finding.Content) == 0 || finding.Start < 1 || finding.End < finding.Start {
					return "Malformed finding.", nil
				}
				switch finding.Severity {
				case "critical", "high":
					mustFix = true
				case "medium", "low":
				default:
					return "Unknown finding severity.", nil
				}
			}
			if mustFix {
				return "Must-fix findings remain; retained in the workflow artifact for editing.", nil
			}
			return "Complete current review; no must-fix findings.", nil
		}
		if response.NextPage == 0 {
			break
		}
		opts.Page = response.NextPage
	}
	return "Waiting for trusted OCR evidence.", nil
}

func artifact(ctx context.Context, c *github.Client, owner, repo string, run int64, name, file string) ([]byte, error) {
	opts := &github.ListOptions{PerPage: 100}
	var found *github.Artifact
	for {
		artifacts, response, err := c.Actions.ListWorkflowRunArtifacts(ctx, owner, repo, run, opts)
		if err != nil {
			return nil, err
		}
		for _, a := range artifacts.Artifacts {
			if a.GetName() == name {
				if found != nil {
					return nil, fmt.Errorf("duplicate artifact")
				}
				found = a
			}
		}
		if response.NextPage == 0 {
			break
		}
		opts.Page = response.NextPage
	}
	if found == nil || found.GetExpired() {
		return nil, fmt.Errorf("missing artifact")
	}
	location, _, err := c.Actions.DownloadArtifact(ctx, owner, repo, found.GetID(), 0)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", location.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact download: %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var output []byte
	for _, entry := range archive.File {
		if entry.Name != file {
			continue
		}
		if output != nil {
			return nil, fmt.Errorf("duplicate file")
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, err
		}
		output, err = io.ReadAll(io.LimitReader(reader, 8<<20))
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("missing file")
	}
	return output, nil
}
