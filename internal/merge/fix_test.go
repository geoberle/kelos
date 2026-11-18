package merge_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v80/github"

	"github.com/hearthctl/kelos-orchestrator/internal/merge"
)

func TestFixRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		// comments is GitHub's list of conversation comments on the pull request
		comments string
		// wantID is the ID of the finding handed to the editor; empty when there is none
		wantID string
	}{
		{
			name:     "assessment that found a defect on the head becomes a finding",
			comments: `[{"id":7,"html_url":"https://github.com/owner/repo/pull/1#issuecomment-7","user":{"login":"controller"},"body":"<!-- kelos-assessment/v1 -->\n{\"attempts\":[{\"task\":\"assessment-a\",\"head\":\"head\",\"state\":\"needs-fix\",\"reason\":\"Removing a Slot deletes an Item on the wrong Page.\"}]}"}]`,
			wantID:   "assessment/owner/repo/1/0/assessment-a",
		},
		{
			// The ID names the newest /reassess on the pull request, so repairs are counted per request.
			name:     "finding after a reassess request names that request",
			comments: `[{"id":7,"html_url":"https://github.com/owner/repo/pull/1#issuecomment-7","user":{"login":"controller"},"body":"<!-- kelos-assessment/v1 -->\n{\"attempts\":[{\"task\":\"assessment-0\",\"head\":\"old\",\"request\":500,\"state\":\"invalidated\",\"reason\":\"PR head changed.\"},{\"task\":\"assessment-a\",\"head\":\"head\",\"state\":\"needs-fix\",\"reason\":\"Removing a Slot deletes an Item on the wrong Page.\"}]}"}]`,
			wantID:   "assessment/owner/repo/1/500/assessment-a",
		},
		{
			name:     "only the newest attempt counts",
			comments: `[{"id":7,"user":{"login":"controller"},"body":"<!-- kelos-assessment/v1 -->\n{\"attempts\":[{\"task\":\"assessment-a\",\"head\":\"head\",\"state\":\"needs-fix\",\"reason\":\"Defect.\"},{\"task\":\"assessment-b\",\"head\":\"head\",\"state\":\"approve\",\"reason\":\"Fine.\"}]}"}]`,
		},
		{
			name:     "defect found on an earlier head is no finding",
			comments: `[{"id":7,"user":{"login":"controller"},"body":"<!-- kelos-assessment/v1 -->\n{\"attempts\":[{\"task\":\"assessment-a\",\"head\":\"old\",\"state\":\"needs-fix\",\"reason\":\"Defect.\"}]}"}]`,
		},
		{
			name:     "request for human judgement is no finding",
			comments: `[{"id":7,"user":{"login":"controller"},"body":"<!-- kelos-assessment/v1 -->\n{\"attempts\":[{\"task\":\"assessment-a\",\"head\":\"head\",\"state\":\"needs-human\",\"reason\":\"Licensing is unclear.\"}]}"}]`,
		},
		{
			name:     "assessment history of another author is no finding",
			comments: `[{"id":7,"user":{"login":"someone"},"body":"<!-- kelos-assessment/v1 -->\n{\"attempts\":[{\"task\":\"assessment-a\",\"head\":\"head\",\"state\":\"needs-fix\",\"reason\":\"Defect.\"}]}"}]`,
		},
		{
			name:     "pull request without an assessment has no finding",
			comments: `[]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" || r.URL.Path != "/repos/owner/repo/issues/1/comments" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				_, _ = w.Write([]byte(tc.comments))
			}))
			defer server.Close()
			client := github.NewClient(server.Client())
			client.BaseURL, _ = url.Parse(server.URL + "/")
			pr := &github.PullRequest{Number: github.Ptr(1), Head: &github.PullRequestBranch{SHA: github.Ptr("head")}}
			finding, err := merge.FixRequest(context.Background(), client, "owner", "repo", pr, "controller")
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.wantID) == 0 {
				if finding != nil {
					t.Fatalf("unexpected finding: %+v", finding)
				}
				return
			}
			if finding == nil || finding.ID != tc.wantID || finding.Content != "The merge assessment found these defects in the pull request. Repair every one of them:\n\nRemoving a Slot deletes an Item on the wrong Page." || finding.Source != "https://github.com/owner/repo/pull/1#issuecomment-7" {
				t.Fatalf("finding=%+v", finding)
			}
		})
	}
}
