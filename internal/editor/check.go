package editor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/go-github/v80/github"
)

const (
	// checkName is the CI check every managed repository reports.
	checkName = "verify"
	// checkPrefix starts the ID of a finding that stands for a failed check.
	checkPrefix = "check/"
	// maxCheckRounds bounds the editor Tasks started for failed checks on one PR.
	maxCheckRounds = 3
	// logLines is how much of the failing job's log an editor is given.
	logLines = 60
)

var (
	logTimestamp = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d:.]+Z `)
	ansiEscape   = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

// failedCheck returns the failed verify check of the PR head as a finding,
// with the ID of its workflow job, or nil when the check has not failed. Only
// the newest run counts, so a check that is running again is not a failure.
func failedCheck(ctx context.Context, c *github.Client, owner, repo string, pr *github.PullRequest) (*Finding, int64, error) {
	head := pr.GetHead().GetSHA()
	runs, _, err := c.Checks.ListCheckRunsForRef(ctx, owner, repo, head, &github.ListCheckRunsOptions{CheckName: github.Ptr(checkName), Filter: github.Ptr("latest")})
	if err != nil {
		return nil, 0, err
	}
	for _, run := range runs.CheckRuns {
		if run.GetApp().GetSlug() != "github-actions" || run.GetName() != checkName || run.GetStatus() != "completed" || run.GetConclusion() != "failure" {
			continue
		}
		return &Finding{
			ID:      fmt.Sprintf("%s%s/%s/%d/%d", checkPrefix, owner, repo, pr.GetNumber(), run.GetID()),
			Content: "The CI check `" + checkName + "` failed on commit " + head + ". It runs `make verify` and then `make e2e` on the pull request merged onto its base branch. Find the cause in the end of the failing job's log below and fix it. `make e2e` needs a container runtime that this pod lacks, so judge its checks from the log.\n\nLog excerpt:\n",
			Source:  run.GetHTMLURL(),
		}, run.GetID(), nil
	}
	return nil, 0, nil
}

// checkLog returns the end of a workflow job's log, up to its last error
// line. The editor's own GitHub identity cannot read workflow logs.
func checkLog(ctx context.Context, c *github.Client, owner, repo string, job int64) (string, error) {
	location, _, err := c.Actions.GetWorkflowJobLogs(ctx, owner, repo, job, 3)
	if err != nil {
		return "", err
	}
	// The location is a signed address on another host; it must not receive
	// the GitHub credentials of c.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("workflow job log: %s", response.Status)
	}
	log, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(log), "\n"), "\n")
	last := len(lines) - 1
	for i := last; i >= 0; i-- {
		if strings.Contains(lines[i], "##[error]") {
			last = i
			break
		}
	}
	lines = lines[max(0, last-logLines+1) : last+1]
	for i, line := range lines {
		lines[i] = ansiEscape.ReplaceAllString(logTimestamp.ReplaceAllString(strings.TrimRight(line, "\r"), ""), "")
	}
	return strings.Join(lines, "\n"), nil
}
