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
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v80/github"

	"github.com/hearthctl/kelos-orchestrator/internal/feedback"
)

// Poll reconciles enrolled PRs in the explicit allowlist.
func Poll(ctx context.Context, c *github.Client, repositories []string, controllerLogin string) error {
	if len(controllerLogin) == 0 || controllerLogin == "geoberle" || controllerLogin == "github-actions[bot]" {
		return fmt.Errorf("a dedicated controller login is required")
	}
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
				if err := reconcile(ctx, c, owner, repo, pr, controllerLogin); err != nil {
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
	// ExcludeReason says why OCR did not review an excluded item.
	ExcludeReason string `json:"exclude_reason,omitempty"`
}
type publication struct {
	identity
	Summary string `json:"summary"`
	Total   *int   `json:"total"`
	Failed  *int   `json:"failed"`
}

type Evidence struct {
	identity
	Summary string `json:"summary"`
	Result  Result `json:"result"`
}

// FindingID identifies one finding of this review run and attempt. Editor
// assignments, dispositions and handled receipts all refer to it.
func (e *Evidence) FindingID(index int) string {
	return fmt.Sprintf("%s/%d/%d/%d/%s/%d", e.Repository, e.PR, e.Run, e.Attempt, e.Head, index)
}

type Finding struct {
	Path     string `json:"path"`
	Start    int    `json:"start_line"`
	End      int    `json:"end_line"`
	Content  string `json:"content"`
	Severity string `json:"severity"`
}
type Result struct {
	Comments []Finding `json:"comments"`
	Manifest struct {
		Schema    string          `json:"schema_version"`
		Operation string          `json:"operation"`
		State     string          `json:"terminal_state"`
		Failure   json.RawMessage `json:"run_failure,omitempty"`
		Input     struct {
			Mode string `json:"mode"`
			Head string `json:"resolved_head"`
			Base string `json:"resolved_base"`
		} `json:"input"`
		Coverage struct {
			Selected  []item `json:"selected"`
			Completed []item `json:"completed"`
			Reused    []item `json:"reused"`
			Failed    []item `json:"failed"`
			Waived    []item `json:"waived"`
			Excluded  []item `json:"excluded"`
		} `json:"coverage"`
	} `json:"manifest"`
}

func reconcile(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest, controllerLogin string) (reconcileErr error) {
	withdrawn := !label(pr, "review-done")
	withdraw := func() error {
		if withdrawn {
			return nil
		}
		_, err := c.Issues.RemoveLabelForIssue(ctx, owner, repo, pr.GetNumber(), "review-done")
		if err == nil {
			withdrawn = true
		}
		return err
	}
	// Any failed validation or mutation withdraws previously published completion.
	defer func() {
		if reconcileErr != nil {
			reconcileErr = errors.Join(reconcileErr, withdraw())
		}
	}()
	decision, err := evaluate(ctx, c, owner, repo, pr, controllerLogin)
	if err != nil {
		return err
	}
	if !decision.Complete {
		if err := withdraw(); err != nil {
			return err
		}
	}
	body := ""
	// Only the configured dedicated controller identity owns status evidence.
	user, _, err := c.Users.Get(ctx, controllerLogin)
	if err != nil {
		return err
	}
	if user.GetID() <= 0 {
		return fmt.Errorf("controller identity unavailable")
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
		}
		if response.NextPage == 0 {
			break
		}
		opts.Page = response.NextPage
	}
	blocked, err := humanFeedback(ctx, c, owner, repo, pr, controllerLogin)
	if err != nil {
		return err
	}
	if blocked {
		decision.Complete = false
		decision.Reason = "Human feedback requires disposition."
	}
	body = fmt.Sprintf("<!-- kelos-review/v1 -->\nHead: %s\n%s", pr.GetHead().GetSHA(), decision.Reason)
	if decision.Evidence != nil {
		encoded, e := json.Marshal(decision.Evidence)
		if e != nil {
			return e
		}
		if len(encoded) > 55000 {
			decision.Complete = false
			decision.Evidence = nil
			body = "<!-- kelos-review/v1 -->\nReview evidence exceeds durable comment capacity; human attention required."
		} else {
			body += "\n\n```json\n" + string(encoded) + "\n```"
		}
	}
	if !decision.Complete {
		if err := withdraw(); err != nil {
			return err
		}
	}
	if decision.Evidence == nil && existing != nil {
		if _, retained, ok := strings.Cut(existing.GetBody(), "\n\n```json\n"); ok {
			body += "\n\n```json\n" + retained
		}
	}
	var published *github.IssueComment
	if existing == nil {
		published, _, err = c.Issues.CreateComment(ctx, owner, repo, pr.GetNumber(), &github.IssueComment{Body: &body})
	} else if existing.GetBody() != body {
		published, _, err = c.Issues.EditComment(ctx, owner, repo, existing.GetID(), &github.IssueComment{Body: &body})
	}
	if err != nil {
		return err
	}
	if !decision.Complete {
		return nil
	}
	if published != nil && published.GetUser().GetID() != user.GetID() {
		return fmt.Errorf("credential does not match controllerLogin")
	}
	latest, err := evaluate(ctx, c, owner, repo, pr, controllerLogin)
	if err != nil {
		return err
	}
	if !latest.Complete || latest.Evidence.Run != decision.Evidence.Run || latest.Evidence.Attempt != decision.Evidence.Attempt {
		return withdraw()
	}
	run, _, err := c.Actions.GetWorkflowRunByID(ctx, owner, repo, decision.Evidence.Run)
	if err != nil {
		return err
	}
	if run.GetRunAttempt() != decision.Evidence.Attempt || run.GetStatus() != "completed" || run.GetConclusion() != "success" {
		return withdraw()
	}
	current, _, err := c.PullRequests.Get(ctx, owner, repo, pr.GetNumber())
	if err != nil {
		return err
	}
	if current.GetHead().GetSHA() != pr.GetHead().GetSHA() || current.GetBase().GetSHA() != pr.GetBase().GetSHA() {
		return withdraw()
	}
	blocked, err = humanFeedback(ctx, c, owner, repo, pr, controllerLogin)
	if err != nil {
		return err
	}
	if blocked || !label(current, "agent-managed") || current.GetState() != "open" {
		return withdraw()
	}
	if label(current, "review-done") {
		return nil
	}
	_, _, err = c.Issues.AddLabelsToIssue(ctx, owner, repo, pr.GetNumber(), []string{"review-done"})
	return err
}

type outcome struct {
	Reason   string
	Complete bool
	Evidence *Evidence
}

func evaluate(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest, controllerLogin string) (outcome, error) {
	workflow, _, err := c.Actions.GetWorkflowByFileName(ctx, owner, repo, "ocr.yml")
	if err != nil {
		return outcome{Reason: ""}, err
	}
	opts := &github.ListWorkflowRunsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		runs, response, err := c.Actions.ListWorkflowRunsByID(ctx, owner, repo, workflow.GetID(), opts)
		if err != nil {
			return outcome{Reason: ""}, err
		}
		for _, run := range runs.WorkflowRuns {
			if run.GetEvent() != "pull_request_target" || run.GetWorkflowID() != workflow.GetID() || run.GetPath() != ".github/workflows/ocr.yml" {
				continue
			}
			// A skipped run reviewed nothing, such as one triggered by another label.
			if run.GetConclusion() == "skipped" {
				continue
			}
			if len(run.PullRequests) > 0 {
				matches := false
				for _, linked := range run.PullRequests {
					if linked.GetNumber() == pr.GetNumber() {
						matches = true
					}
				}
				if !matches {
					continue
				}
			} else if len(run.GetDisplayTitle()) > 0 && run.GetDisplayTitle() != fmt.Sprintf("OCR PR #%d", pr.GetNumber()) {
				continue
			}
			data, err := artifact(ctx, c, owner, repo, run.GetID(), fmt.Sprintf("kelos-ocr-identity-%d-%d", run.GetID(), run.GetRunAttempt()), "identity.json")
			if err != nil {
				return outcome{Reason: "Missing trusted correlation evidence; manual rerun required."}, nil
			}
			var id identity
			if decodeEvidence(data, &id) != nil || id.Repository != owner+"/"+repo || id.Run != run.GetID() || id.Attempt != run.GetRunAttempt() {
				return outcome{Reason: "Malformed correlation evidence; manual rerun required."}, nil
			}
			if id.PR != pr.GetNumber() {
				continue
			}
			if id.Head != pr.GetHead().GetSHA() {
				return outcome{Reason: "Waiting for review of current head."}, nil
			}
			if run.GetStatus() != "completed" || run.GetConclusion() != "success" {
				return outcome{Reason: "Review unfinished or publication failed; manual rerun required."}, nil
			}
			raw, err := artifact(ctx, c, owner, repo, run.GetID(), fmt.Sprintf("ocr-review-result-%d-%d", run.GetID(), run.GetRunAttempt()), "ocr-result.json")
			if err != nil {
				return outcome{Reason: "Missing review artifact; manual rerun required."}, nil
			}
			var r Result
			var fields map[string]json.RawMessage
			if decodeEvidence(raw, &r) != nil || json.Unmarshal(raw, &fields) != nil || len(fields["comments"]) == 0 {
				return outcome{Reason: "Malformed review artifact; manual rerun required."}, nil
			}
			m := r.Manifest
			if m.Schema != "ocr.run-manifest/v1" || m.Operation != "review" || m.Input.Mode != "range" || m.Input.Head != id.Head || len(id.Base) == 0 {
				return outcome{Reason: "Invalid review identity; manual rerun required."}, nil
			}
			if m.State == "skipped" {
				return outcome{Reason: "Review skipped; human LGTM required."}, nil
			}
			if m.State != "complete" || (len(m.Failure) > 0 && string(m.Failure) != "null") {
				return outcome{Reason: "Review incomplete; manual rerun required."}, nil
			}
			pubRaw, err := artifact(ctx, c, owner, repo, run.GetID(), fmt.Sprintf("kelos-ocr-publication-%d-%d", run.GetID(), run.GetRunAttempt()), "publication.json")
			if err != nil {
				return outcome{Reason: "Missing publication evidence; manual rerun required."}, nil
			}
			var pub publication
			if decodeEvidence(pubRaw, &pub) != nil || pub.Repository != id.Repository || pub.PR != id.PR || pub.Run != id.Run || pub.Attempt != id.Attempt || pub.Head != id.Head || pub.Base != m.Input.Base || pub.Total == nil || *pub.Total != len(r.Comments) || pub.Failed == nil || *pub.Failed != 0 {
				return outcome{Reason: "Review publication incomplete; manual rerun required."}, nil
			}
			summaryURL, err := url.Parse(pub.Summary)
			if err != nil || summaryURL.Scheme != "https" || summaryURL.Host != "github.com" || (summaryURL.Path != fmt.Sprintf("/%s/%s/pull/%d", owner, repo, id.PR) && summaryURL.Path != fmt.Sprintf("/%s/%s/issues/%d", owner, repo, id.PR)) || !strings.HasPrefix(summaryURL.Fragment, "issuecomment-") {
				return outcome{Reason: "Invalid publication summary."}, nil
			}
			summaryID, err := strconv.ParseInt(strings.TrimPrefix(summaryURL.Fragment, "issuecomment-"), 10, 64)
			if err != nil || summaryID <= 0 {
				return outcome{Reason: "Invalid publication summary."}, nil
			}
			summary, _, err := c.Issues.GetComment(ctx, owner, repo, summaryID)
			if err != nil {
				return outcome{}, err
			}
			if summary.GetUser().GetLogin() != "github-actions[bot]" || !strings.HasPrefix(summary.GetBody(), "<!-- ocr-summary -->") {
				return outcome{Reason: "Untrusted publication summary."}, nil
			}
			if len(raw) > 48000 {
				return outcome{Reason: "Review evidence exceeds durable comment capacity; human attention required."}, nil
			}
			retained := &Evidence{identity: id, Summary: pub.Summary, Result: r}
			cov := m.Coverage
			if cov.Selected == nil || cov.Completed == nil || cov.Reused == nil || cov.Failed == nil || cov.Waived == nil || cov.Excluded == nil || len(cov.Selected) == 0 || len(cov.Failed)+len(cov.Waived) > 0 {
				return outcome{Reason: "Coverage incomplete or excluded; human attention required."}, nil
			}
			currentRange, _, err := c.Repositories.CompareCommits(ctx, owner, repo, pr.GetBase().GetSHA(), id.Head, nil)
			if err != nil {
				return outcome{Reason: ""}, err
			}
			if currentRange.GetMergeBaseCommit().GetSHA() != m.Input.Base {
				return outcome{Reason: "Review range is stale."}, nil
			}
			// OCR excludes deleted files because they have no content to
			// review. Any other exclusion is a coverage gap.
			deleted := map[string]bool{}
			for _, i := range cov.Excluded {
				if i.ExcludeReason != "deleted" || len(i.Path) == 0 {
					return outcome{Reason: "Coverage incomplete or excluded; human attention required."}, nil
				}
				deleted[i.Path] = true
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
					return outcome{Reason: ""}, err
				}
				for _, file := range files {
					fileCount++
					// A deletion claimed by OCR counts only when GitHub
					// reports the same file as removed.
					if deleted[file.GetFilename()] && file.GetStatus() == "removed" {
						delete(deleted, file.GetFilename())
						continue
					}
					if !paths[file.GetFilename()] {
						return outcome{Reason: "Changed file omitted from review coverage."}, nil
					}
					delete(paths, file.GetFilename())
				}
				if response.NextPage == 0 {
					break
				}
				fileOptions.Page = response.NextPage
			}
			if len(paths) > 0 || len(deleted) > 0 || fileCount >= 3000 {
				return outcome{Reason: "Coverage cannot be verified."}, nil
			}
			selected := map[string]string{}
			for _, i := range cov.Selected {
				if len(i.ID) == 0 || len(i.Path) == 0 || len(selected[i.ID]) > 0 {
					return outcome{Reason: "Malformed coverage."}, nil
				}
				selected[i.ID] = i.Path
			}
			for _, i := range append(cov.Completed, cov.Reused...) {
				if selected[i.ID] != i.Path || len(i.Path) == 0 {
					return outcome{Reason: "Malformed coverage."}, nil
				}
				delete(selected, i.ID)
			}
			if len(selected) > 0 {
				return outcome{Reason: "Incomplete coverage."}, nil
			}
			for _, finding := range r.Comments {
				if len(finding.Path) == 0 || len(finding.Content) == 0 || finding.Start < 0 || finding.End < finding.Start {
					return outcome{Reason: "Malformed finding."}, nil
				}
				covered := false
				for _, i := range cov.Selected {
					if i.Path == finding.Path {
						covered = true
						break
					}
				}
				if !covered {
					return outcome{Reason: "Finding outside reviewed coverage."}, nil
				}
				switch finding.Severity {
				case "critical", "high", "medium", "low":
				default:
					return outcome{Reason: "Unknown finding severity."}, nil
				}
			}
			if len(r.Comments) == 0 {
				return outcome{Reason: "Complete current review; no findings.", Complete: true, Evidence: retained}, nil
			}
			// Every finding needs an editor disposition, whatever its severity.
			// One answered without a commit triggers no new review, so the
			// editor's receipt is what closes it.
			handled, err := feedback.Handled(ctx, c, owner, repo, pr.GetNumber(), id.Head, controllerLogin)
			if err != nil {
				return outcome{Reason: ""}, err
			}
			for index := range r.Comments {
				if !handled[retained.FindingID(index)] {
					return outcome{Reason: "Findings remain; evidence retained for editing.", Evidence: retained}, nil
				}
			}
			return outcome{Reason: "Complete current review; every finding addressed.", Complete: true, Evidence: retained}, nil
		}
		if response.NextPage == 0 {
			break
		}
		opts.Page = response.NextPage
	}
	return outcome{Reason: "Waiting for trusted OCR evidence."}, nil
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
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact download: %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("artifact exceeds download limit")
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
		output, err = io.ReadAll(io.LimitReader(reader, (8<<20)+1))
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if len(output) > 8<<20 {
			return nil, fmt.Errorf("artifact file exceeds limit")
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

func humanFeedback(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest, controller string) (bool, error) {
	pending, err := feedback.Pending(ctx, c, owner, repo, pr.GetNumber(), pr.GetHead().GetSHA(), controller)
	return len(pending) > 0, err
}

// OCR is model output. Reject ambiguous objects before typed decoding; Go's
// decoder otherwise silently accepts duplicate (including case-folded) keys.
func decodeEvidence(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			keys := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("invalid object key")
				}
				folded := strings.ToLower(name)
				if keys[folded] {
					return fmt.Errorf("duplicate object key %q", name)
				}
				keys[folded] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return json.Unmarshal(data, target)
}

// CurrentEvidence revalidates the trusted workflow and returns current findings.
// Historical status comments alone cannot authorize a new editor.
func CurrentEvidence(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest, controllerLogin string) (*Evidence, error) {
	decision, err := evaluate(ctx, c, owner, repo, pr, controllerLogin)
	if err != nil || decision.Evidence == nil {
		return nil, err
	}
	return decision.Evidence, nil
}

// Complete revalidates the current review and durable human dispositions before
// an assessment starts. A label or retained status text alone is insufficient.
func Complete(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest, controller string) (bool, error) {
	decision, err := evaluate(ctx, c, owner, repo, pr, controller)
	if err != nil || !decision.Complete {
		return false, err
	}
	pending, err := feedback.Pending(ctx, c, owner, repo, pr.GetNumber(), pr.GetHead().GetSHA(), controller)
	return err == nil && len(pending) == 0, err
}
