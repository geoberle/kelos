package review_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/go-github/v80/github"

	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

func TestVerifiedReviewEvidence(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		failed               int
		wantDone             bool
		resultFrom, resultTo string
		changedHead          bool
		attempt              int
		actor                string
	}{
		{"reassessment command does not become feedback", 0, true, "", "", false, 2, "controller"},
		{"complete published review survives restart", 0, true, "", "", false, 2, "controller"},
		{"green workflow with publication failures does not complete", 1, false, "", "", false, 2, "controller"},
		{"duplicate JSON fields cannot complete", 0, false, `"operation":"review"`, `"operation":"bad","operation":"review"`, false, 2, "controller"},
		{"must-fix blocks completion", 0, false, `"comments":[]`, `"comments":[{"path":"main.go","start_line":1,"end_line":1,"content":"unsafe","severity":"high"}]`, false, 2, "controller"},
		{"advisory permits completion", 0, true, `"comments":[]`, `"comments":[{"path":"main.go","start_line":0,"end_line":0,"content":"simplify","severity":"low"}]`, false, 2, "controller"},
		{"unknown severity blocks completion", 0, false, `"comments":[]`, `"comments":[{"path":"main.go","start_line":1,"end_line":1,"content":"unsafe","severity":"warning"}]`, false, 2, "controller"},
		{"explicit null findings is a valid empty result", 0, true, `"comments":[]`, `"comments":null`, false, 2, "controller"},
		{"new human feedback prevents completion", 0, false, "", "", false, 2, "controller"},
		{"unrelated missing identity does not starve PR", 0, true, "", "", false, 2, "controller"},
		{"paginated changed files are all covered", 0, true, "", "", false, 2, "controller"},
		{"expired artifact cannot complete", 0, false, "", "", false, 2, "controller"},
		{"wrong PR identity cannot complete", 0, false, "", "", false, 2, "controller"},
		{"new workflow run invalidates completion", 0, false, "", "", false, 2, "controller"},
		{"wrong publishing identity cannot complete", 0, false, "", "", false, 2, "controller"},
		{"partial cannot complete", 0, false, `"complete"`, `"partial"`, false, 2, "controller"},
		{"skipped cannot complete", 0, false, `"complete"`, `"skipped"`, false, 2, "controller"},
		{"missing coverage cannot complete", 0, false, `"completed":[{"item_id":"file-1","path":"main.go"}]`, `"completed":[]`, false, 2, "controller"},
		{"stale review cannot complete", 0, false, `"resolved_head":"head"`, `"resolved_head":"old"`, false, 2, "controller"},
		{"new head before publication cannot complete", 0, false, "", "", true, 2, "controller"},
		{"rerun invalidates evidence", 0, false, "", "", false, 3, "controller"},
		{"installation token uses configured bot identity", 0, true, "", "", false, 2, "controller[bot]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := os.ReadFile("testdata/complete.json")
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.resultFrom) > 0 {
				result = []byte(strings.ReplaceAll(string(result), tc.resultFrom, tc.resultTo))
			}
			total := 0
			if strings.Contains(string(result), `"severity"`) {
				total = 1
			}
			archives := map[string][]byte{}
			for name, body := range map[string]string{
				"identity.json":    `{"repository":"owner/repo","pr":1,"run":10,"attempt":2,"head":"head","base":"base"}`,
				"ocr-result.json":  string(result),
				"publication.json": fmt.Sprintf(`{"repository":"owner/repo","pr":1,"run":10,"attempt":2,"head":"head","base":"base","summary":"https://github.com/owner/repo/issues/1#issuecomment-50","total":%d,"failed":%d}`, total, tc.failed),
			} {
				if tc.name == "wrong PR identity cannot complete" && name == "identity.json" {
					body = strings.ReplaceAll(body, `"pr":1`, `"pr":2`)
				}
				var b bytes.Buffer
				z := zip.NewWriter(&b)
				f, e := z.Create(name)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.Write([]byte(body)); e != nil {
					t.Fatal(e)
				}
				if e = z.Close(); e != nil {
					t.Fatal(e)
				}
				archives[name] = b.Bytes()
			}
			runReads := 0
			commentReads := 0
			done := false
			status := ""
			posts := 0
			pr := func() string {
				labels := `{"name":"agent-managed"}`
				if done {
					labels += `,{"name":"review-done"}`
				}
				return `{"number":1,"state":"open","head":{"sha":"head"},"base":{"sha":"base"},"labels":[` + labels + `]}`
			}
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "POST" && r.URL.Path == "/repos/owner/repo/issues/1/labels" {
					done = true
					_, _ = w.Write([]byte(`[]`))
					return
				}
				if r.Method == "DELETE" {
					done = false
					w.WriteHeader(204)
					return
				}
				if r.Method == "POST" || r.Method == "PATCH" {
					var c github.IssueComment
					if e := json.NewDecoder(r.Body).Decode(&c); e != nil {
						t.Error(e)
					}
					status = c.GetBody()
					if r.Method == "POST" {
						posts++
					}
					if tc.name == "wrong publishing identity cannot complete" {
						_, _ = w.Write([]byte(`{"id":60,"user":{"id":91}}`))
						return
					}
					_, _ = w.Write([]byte(`{"id":60,"user":{"id":90,"login":"controller"}}`))
					return
				}
				var body string
				switch r.URL.Path {
				case "/repos/owner/repo/collaborators/geoberle/permission":
					body = `{"role_name":"admin"}`
				case "/repos/owner/repo/pulls":
					body = "[" + pr() + "]"
				case "/repos/owner/repo/pulls/1":
					body = pr()
					if tc.changedHead {
						body = strings.ReplaceAll(body, `"sha":"head"`, `"sha":"new"`)
					}
				case "/repos/owner/repo/actions/workflows/ocr.yml":
					body = `{"id":8}`
				case "/repos/owner/repo/actions/workflows/8/runs":
					body = `{"workflow_runs":[{"id":10,"workflow_id":8,"path":".github/workflows/ocr.yml","event":"pull_request_target","run_attempt":2,"status":"completed","conclusion":"success","pull_requests":[{"number":1}]}]}`
				case "/repos/owner/repo/actions/runs/10":
					body = fmt.Sprintf(`{"id":10,"run_attempt":%d,"status":"completed","conclusion":"success"}`, tc.attempt)
				case "/repos/owner/repo/actions/runs/11/artifacts":
					body = `{"artifacts":[]}`
				case "/repos/owner/repo/actions/runs/10/artifacts":
					body = `{"artifacts":[{"id":1,"name":"kelos-ocr-identity-10-2"},{"id":2,"name":"ocr-review-result-10-2"},{"id":3,"name":"kelos-ocr-publication-10-2"}]}`
				case "/repos/owner/repo/actions/artifacts/1/zip":
					http.Redirect(w, r, server.URL+"/download/identity.json", http.StatusFound)
					return
				case "/repos/owner/repo/actions/artifacts/2/zip":
					http.Redirect(w, r, server.URL+"/download/ocr-result.json", http.StatusFound)
					return
				case "/repos/owner/repo/actions/artifacts/3/zip":
					http.Redirect(w, r, server.URL+"/download/publication.json", http.StatusFound)
					return
				case "/download/identity.json", "/download/ocr-result.json", "/download/publication.json":
					_, _ = w.Write(archives[strings.TrimPrefix(r.URL.Path, "/download/")])
					return
				case "/repos/owner/repo/compare/base...head":
					body = `{"merge_base_commit":{"sha":"base"}}`
				case "/repos/owner/repo/pulls/1/files":
					body = `[{"filename":"main.go"}]`
				case "/users/controller", "/users/controller[bot]":
					body = fmt.Sprintf(`{"id":90,"login":%q}`, tc.actor)
				case "/repos/owner/repo/issues/1/comments":
					if len(status) == 0 {
						body = "[]"
					} else {
						b, _ := json.Marshal([]github.IssueComment{{ID: github.Ptr(int64(60)), Body: &status, User: &github.User{ID: github.Ptr(int64(90)), Login: github.Ptr(tc.actor)}}})
						body = string(b)
					}
				case "/repos/owner/repo/pulls/1/comments", "/repos/owner/repo/pulls/1/reviews":
					body = "[]"
				case "/repos/owner/repo/issues/comments/50":
					body = `{"id":50,"body":"<!-- ocr-summary -->\n<!-- ocr-summary-run:10:2 -->","user":{"login":"github-actions[bot]"}}`
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
					return
				}
				if r.URL.Path == "/repos/owner/repo/actions/workflows/8/runs" {
					runReads++
					if tc.name == "new workflow run invalidates completion" && runReads > 1 {
						body = strings.ReplaceAll(body, `"id":10`, `"id":11`)
					}
				}
				if tc.name == "unrelated missing identity does not starve PR" && r.URL.Path == "/repos/owner/repo/actions/workflows/8/runs" {
					body = strings.Replace(body, `"workflow_runs":[`, `"workflow_runs":[{"id":11,"workflow_id":8,"path":".github/workflows/ocr.yml","event":"pull_request_target","pull_requests":[{"number":2}]},`, 1)
				}
				if tc.name == "expired artifact cannot complete" && r.URL.Path == "/repos/owner/repo/actions/runs/10/artifacts" {
					body = strings.ReplaceAll(body, `"id":1,`, `"id":1,"expired":true,`)
				}
				if tc.name == "paginated changed files are all covered" && r.URL.Path == "/repos/owner/repo/pulls/1/files" && r.URL.Query().Get("page") != "2" {
					w.Header().Set("Link", "<"+server.URL+"/repos/owner/repo/pulls/1/files?page=2>; rel=\"next\"")
					body = "[]"
				}
				if tc.name == "reassessment command does not become feedback" && r.URL.Path == "/repos/owner/repo/issues/1/comments" {
					body = strings.TrimSuffix(body, "]") + `,{"id":80,"body":"reassess aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","user":{"id":100,"login":"geoberle"}}]`
					body = strings.Replace(body, "[,", "[", 1)
				}
				if tc.name == "new human feedback prevents completion" && r.URL.Path == "/repos/owner/repo/issues/1/comments" {
					commentReads++
					if commentReads >= 3 {
						body = `[{"id":80,"body":"Please fix the race","user":{"id":100,"login":"geoberle"}}]`
					}
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			rounds := 2
			if tc.name == "wrong publishing identity cannot complete" {
				rounds = 1
			}
			for i := 0; i < rounds; i++ {
				client := github.NewClient(server.Client())
				client.BaseURL, _ = url.Parse(server.URL + "/")
				if err := review.Poll(context.Background(), client, []string{"owner/repo"}, tc.actor); err != nil && tc.name != "wrong publishing identity cannot complete" {
					t.Fatal(err)
				}
				if done != tc.wantDone {
					t.Fatalf("round %d: review-done=%v want %v", i, done, tc.wantDone)
				}
			}
			if done != tc.wantDone {
				t.Fatalf("review-done=%v want %v; status=%s", done, tc.wantDone, status)
			}
			if tc.name != "new human feedback prevents completion" && posts != 1 {
				t.Fatalf("status comments=%d want 1", posts)
			}
			if tc.name == "complete published review survives restart" {
				want, err := os.ReadFile("testdata/complete.golden")
				if err != nil {
					t.Fatal(err)
				}
				if status != strings.TrimSuffix(string(want), "\n") {
					t.Fatalf("status differs from golden:\n%s", status)
				}
			}
			if tc.wantDone && !strings.Contains(status, `"run":10`) {
				t.Fatalf("missing durable run evidence: %s", status)
			}
		})
	}
}
