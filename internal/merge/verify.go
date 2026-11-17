package merge

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/google/go-github/v80/github"
)

const (
	// verifyContext is the commit status in which the controller records the
	// verify result of a pull request head on a base commit.
	verifyContext = "verify-base"
	// verifyWorkflow is the caller workflow every repository keeps.
	verifyWorkflow = "ci.yml"
	// baseDelimiter precedes the tested base in the status description, as in Prow.
	baseDelimiter = " BaseSHA:"
)

var (
	// errVerifying reports that the merge waits for the verify result on the current base.
	errVerifying = errors.New("waiting for the verify result on the current base")
	// errUnverified reports that no verify result exists for the current base.
	errUnverified = errors.New("no verify result for the current base")
)

// verified requires a green verify result for the pull request head on the
// commit its base branch points to now. Only the controller's own statuses
// count. A pending result is settled from its workflow run.
func verified(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest, controller string) error {
	user, _, err := c.Users.Get(ctx, controller)
	if err != nil {
		return err
	}
	base, err := baseCommit(ctx, c, owner, repo, pr.GetBase().GetRef())
	if err != nil {
		return err
	}
	head := pr.GetHead().GetSHA()
	// GitHub returns statuses newest first.
	statuses, _, err := c.Repositories.ListStatuses(ctx, owner, repo, head, &github.ListOptions{PerPage: 100})
	if err != nil {
		return err
	}
	var result *github.RepoStatus
	for _, status := range statuses {
		if status.GetContext() == verifyContext && status.GetCreator().GetID() == user.GetID() {
			result = status
			break
		}
	}
	if result == nil {
		return errUnverified
	}
	if _, tested, _ := strings.Cut(result.GetDescription(), baseDelimiter); tested != base {
		return errUnverified
	}
	state := result.GetState()
	if state == "pending" {
		id, err := strconv.ParseInt(path.Base(result.GetTargetURL()), 10, 64)
		if err != nil {
			return fmt.Errorf("verify result names no workflow run: %w", err)
		}
		run, _, err := c.Actions.GetWorkflowRunByID(ctx, owner, repo, id)
		if err != nil {
			return err
		}
		if run.GetStatus() != "completed" {
			return errVerifying
		}
		description := "verify failed"
		state = "failure"
		if run.GetConclusion() == "success" {
			description = "verify passed"
			state = "success"
		}
		if _, _, err := c.Repositories.CreateStatus(ctx, owner, repo, head, github.RepoStatus{Context: github.Ptr(verifyContext), State: &state, Description: github.Ptr(description + baseDelimiter + base), TargetURL: result.TargetURL}); err != nil {
			return err
		}
		if state == "success" {
			// GitHub has yet to recompute the mergeable state; merge on the next poll.
			return errVerifying
		}
	}
	if state != "success" {
		return fmt.Errorf("verify failed on base %s", base)
	}
	return nil
}

// retest runs verify for the pull request head on the current base commit and
// records the run as the pending result, so it is triggered once.
func retest(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest) error {
	base, err := baseCommit(ctx, c, owner, repo, pr.GetBase().GetRef())
	if err != nil {
		return err
	}
	head := pr.GetHead().GetSHA()
	request, err := c.NewRequest("POST", fmt.Sprintf("repos/%s/%s/actions/workflows/%s/dispatches", owner, repo, verifyWorkflow), map[string]any{
		"ref":                pr.GetBase().GetRef(),
		"inputs":             map[string]string{"head": head, "base": base},
		"return_run_details": true,
	})
	if err != nil {
		return err
	}
	var run struct {
		HTMLURL string `json:"html_url"`
	}
	if _, err := c.Do(ctx, request, &run); err != nil {
		return err
	}
	if len(run.HTMLURL) == 0 {
		return errors.New("verify run unavailable")
	}
	_, _, err = c.Repositories.CreateStatus(ctx, owner, repo, head, github.RepoStatus{Context: github.Ptr(verifyContext), State: github.Ptr("pending"), Description: github.Ptr("verify is running" + baseDelimiter + base), TargetURL: &run.HTMLURL})
	return err
}

func baseCommit(ctx context.Context, c *github.Client, owner, repo, branch string) (string, error) {
	ref, _, err := c.Git.GetRef(ctx, owner, repo, "heads/"+branch)
	if err != nil {
		return "", err
	}
	return ref.GetObject().GetSHA(), nil
}

// reportConflict keeps the needs-rebase label on exactly the pull requests
// that conflict with their base. Mergeable is unset while GitHub computes it.
func reportConflict(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest) error {
	if pr.Mergeable == nil {
		return nil
	}
	conflict := pr.GetMergeableState() == "dirty"
	if conflict && !label(pr, "needs-rebase") {
		_, _, err := c.Issues.AddLabelsToIssue(ctx, owner, repo, pr.GetNumber(), []string{"needs-rebase"})
		return err
	}
	if !conflict && label(pr, "needs-rebase") {
		_, err := c.Issues.RemoveLabelForIssue(ctx, owner, repo, pr.GetNumber(), "needs-rebase")
		return err
	}
	return nil
}
