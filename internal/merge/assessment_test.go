package merge_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v80/github"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	merge "github.com/hearthctl/kelos-orchestrator/internal/merge"
	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

func TestCleanReviewCreatesAssessment(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		response, state               string
		retry, denied, edited, failed bool
		stale                         bool
		changedHead                   bool
		badOCR                        string
	}{
		{name: "clean current review creates retained assessment", state: "pending"},
		{name: "new head during publication invalidates approval", response: `{"commit":"head","recommendation":"approve","reason":"Tested."}`, state: "invalidated", changedHead: true},
		{name: "later failed OCR invalidates approval", response: `{"commit":"head","recommendation":"approve","reason":"Tested."}`, state: "invalidated", badOCR: "failed"},
		{name: "later skipped OCR invalidates approval", response: `{"commit":"head","recommendation":"approve","reason":"Tested."}`, state: "invalidated", badOCR: "skipped"},
		{name: "later partial OCR invalidates approval", response: `{"commit":"head","recommendation":"approve","reason":"Tested."}`, state: "invalidated", badOCR: "partial"},
		{name: "later must-fix OCR invalidates approval", response: `{"commit":"head","recommendation":"approve","reason":"Tested."}`, state: "invalidated", badOCR: "must-fix"},
		{name: "unfinished OCR run keeps approval", response: `{"commit":"head","recommendation":"approve","reason":"Tested."}`, state: "approve", badOCR: "unfinished"},
		{name: "valid assessment", response: `{"commit":"head","recommendation":"approve","reason":"Small tested change."}`, state: "approve"},
		{name: "approval survives absent review-done label", response: `{"commit":"head","recommendation":"approve","reason":"Small tested change."}`, state: "approve"},
		{name: "needs human remains terminal", response: `{"commit":"head","recommendation":"needs-human","reason":"Migration risk."}`, state: "needs-human"},
		{name: "malformed result remains terminal", response: `not json`, state: "malformed"},
		{name: "stale result", response: `{"commit":"old","recommendation":"approve","reason":"Tested."}`, state: "malformed"},
		{name: "duplicate result key", response: `{"commit":"old","commit":"head","recommendation":"approve","reason":"Tested."}`, state: "malformed"},
		{name: "explicit retry after malformed result", response: `invalid`, state: "pending", retry: true},
		{name: "unauthorized retry", response: `invalid`, state: "malformed", retry: true, denied: true},
		{name: "retry request older than the assessment", response: `invalid`, state: "malformed", retry: true, stale: true},
		{name: "edited retry", response: `invalid`, state: "malformed", retry: true, edited: true},
		{name: "failed task is not automatically retried", state: "failed", failed: true},
		{name: "trailing result", response: `{"commit":"head","recommendation":"approve","reason":"Tested."} {}`, state: "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sha := strings.Repeat("a", 40)
			requestVisible := false
			currentPoll := 0
			prReads := 0
			result, err := os.ReadFile("../review/testdata/complete.json")
			if err != nil {
				t.Fatal(err)
			}
			total := 0
			archives := map[string][]byte{}
			for name, body := range map[string]string{
				"identity.json":    `{"repository":"owner/repo","pr":1,"run":10,"attempt":2,"head":"head","base":"base"}`,
				"ocr-result.json":  string(result),
				"publication.json": fmt.Sprintf(`{"repository":"owner/repo","pr":1,"run":10,"attempt":2,"head":"head","base":"base","summary":"https://github.com/owner/repo/issues/1#issuecomment-50","total":%d,"failed":%d}`, total, 0),
			} {
				body = strings.ReplaceAll(body, `:"head"`, `:"`+sha+`"`)
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
			var savedComments []*github.IssueComment
			var task map[string]any
			creates := 0
			approved := false
			pr := func() string {
				labels := `{"name":"agent-managed"},{"name":"review-done"}`
				if tc.name == "approval survives absent review-done label" && currentPoll >= 2 {
					labels = `{"name":"agent-managed"}`
				}
				if approved {
					labels += `,{"name":"lgtm"}`
				}
				return `{"number":1,"state":"open","mergeable":true,"mergeable_state":"clean","head":{"sha":"head","ref":"agent-fix","repo":{"full_name":"owner/repo"}},"base":{"sha":"base","ref":"main"},"labels":[` + labels + `]}`
			}

			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.URL.Path = strings.ReplaceAll(r.URL.Path, sha, "head")
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/repos/owner/repo/issues/1/labels" && r.Method == "POST" {
					approved = true
					_, _ = w.Write([]byte(`[]`))
					return
				}
				if r.URL.Path == "/repos/owner/repo/issues/1/labels/lgtm" && r.Method == "DELETE" {
					approved = false
					w.WriteHeader(204)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks/") {
					if task == nil || !strings.HasSuffix(r.URL.Path, "/"+task["metadata"].(map[string]any)["name"].(string)) {
						w.WriteHeader(404)
						_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`))
						return
					}
					_ = json.NewEncoder(w).Encode(task)
					return
				}
				if r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/repos/owner/repo/issues/comments/") {
					var update github.IssueComment
					_ = json.NewDecoder(r.Body).Decode(&update)
					for _, comment := range savedComments {
						if r.URL.Path == fmt.Sprintf("/repos/owner/repo/issues/comments/%d", comment.GetID()) {
							comment.Body = update.Body
							_ = json.NewEncoder(w).Encode(comment)
							return
						}
					}
					w.WriteHeader(404)
					return
				}
				if r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks" {
					if r.Method == "POST" {
						creates++
						if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
							t.Error(err)
						}
						task["status"] = map[string]any{"phase": "Pending"}
						task["metadata"].(map[string]any)["uid"] = "task-uid"
						_ = json.NewEncoder(w).Encode(task)
						return
					}
					items := []any{}
					if task != nil {
						items = append(items, task)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "kelos.dev/v1alpha2", "kind": "TaskList", "items": items})
					return
				}

				var body string
				switch r.URL.Path {
				case "/repos/owner/repo/rules/branches/main":
					body = `[{"type":"pull_request","parameters":{"required_approving_review_count":0}}]`
				case "/repos/owner/repo/commits/head/check-runs":
					body = `{"check_runs":[{"name":"ci","head_sha":"head","status":"completed","conclusion":"success","app":{"id":1}}]}`
				case "/repos/owner/repo/commits/head/statuses":
					body = `[]`
				case "/users/controller":
					body = `{"id":90,"login":"controller"}`
				case "/repos/owner/repo/pulls":
					body = "[" + pr() + "]"
				case "/repos/owner/repo/pulls/1":
					body = pr()
					prReads++
					if tc.changedHead && currentPoll == 1 && prReads >= 2 {
						body = strings.ReplaceAll(body, `"sha":"head"`, `"sha":"new-head"`)
					}
				case "/repos/owner/repo/actions/workflows/ocr.yml":
					body = `{"id":8}`
				case "/repos/owner/repo/actions/workflows/8/runs":

					body = `{"workflow_runs":[{"id":10,"workflow_id":8,"path":".github/workflows/ocr.yml","event":"pull_request_target","run_attempt":2,"status":"completed","conclusion":"success","pull_requests":[{"number":1}]}]}`
					if tc.badOCR == "failed" && currentPoll >= 2 {
						body = strings.ReplaceAll(body, "success", "failure")
					}
					// Adding lgtm triggers a run that is listed for a few seconds before it is skipped.
					if tc.badOCR == "unfinished" && currentPoll >= 2 {
						body = strings.Replace(body, `"workflow_runs":[`, `"workflow_runs":[{"id":11,"workflow_id":8,"path":".github/workflows/ocr.yml","event":"pull_request_target","run_attempt":1,"status":"in_progress","pull_requests":[{"number":1}]},`, 1)
					}
				case "/repos/owner/repo/actions/runs/10":
					body = fmt.Sprintf(`{"id":10,"run_attempt":%d,"status":"completed","conclusion":"success"}`, 2)
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
					if len(tc.badOCR) > 0 && tc.badOCR != "unfinished" && currentPoll >= 2 && r.URL.Path == "/download/ocr-result.json" {
						changed := strings.ReplaceAll(string(result), `:"head"`, `:"`+sha+`"`)
						if tc.badOCR == "must-fix" {
							changed = strings.ReplaceAll(changed, `"comments":[]`, `"comments":[{"path":"main.go","start_line":1,"end_line":1,"content":"Must fix correctness regression.","severity":"must-fix"}]`)
						} else {
							changed = strings.ReplaceAll(changed, `"terminal_state":"complete"`, `"terminal_state":"`+tc.badOCR+`"`)
						}
						var buf bytes.Buffer
						z := zip.NewWriter(&buf)
						f, _ := z.Create("ocr-result.json")
						_, _ = f.Write([]byte(changed))
						_ = z.Close()
						_, _ = w.Write(buf.Bytes())
						return
					}
					_, _ = w.Write(archives[strings.TrimPrefix(r.URL.Path, "/download/")])
					return
				case "/repos/owner/repo/compare/base...head":
					body = `{"merge_base_commit":{"sha":"base"}}`
				case "/repos/owner/repo/pulls/1/files":
					body = `[{"filename":"main.go"}]`
				case "/repos/owner/repo/issues/1/comments":
					if r.Method == "POST" {
						var comment github.IssueComment
						if err := json.NewDecoder(r.Body).Decode(&comment); err != nil {
							t.Error(err)
						}
						comment.ID = github.Ptr(int64(90 + len(savedComments)))
						comment.User = &github.User{Login: github.Ptr("controller")}
						savedComments = append(savedComments, &comment)
						_ = json.NewEncoder(w).Encode(comment)
						return
					}
					comments := append([]*github.IssueComment{}, savedComments...)
					if requestVisible {
						var command github.IssueComment
						// A request counts only when it is newer than the assessment it answers.
						created := "2099-01-01T00:00:00Z"
						if tc.stale {
							created = "2026-11-15T00:00:00Z"
						}
						_ = json.Unmarshal([]byte(`{"id":200,"body":"/reassess","user":{"login":"geoberle"},"created_at":"`+created+`","updated_at":"`+created+`"}`), &command)
						if tc.edited {
							command.UpdatedAt = github.Ptr(github.Timestamp{Time: command.GetCreatedAt().Add(time.Second)})
						}
						comments = append(comments, &command)
					}
					encoded, _ := json.Marshal(comments)
					body = string(encoded)
				case "/repos/owner/repo/collaborators/geoberle/permission":
					body = `{"role_name":"admin"}`
					if tc.denied {
						body = `{"role_name":"read"}`
					}
				case "/repos/owner/repo/issues/1/timeline", "/repos/owner/repo/pulls/1/reviews":
					body = "[]"
					if r.Method == "POST" {
						body = `{}`
					}
				case "/repos/owner/repo/pulls/1/comments":
					body = "[]"

				case "/repos/owner/repo/issues/comments/50":
					body = `{"id":50,"body":"<!-- ocr-summary -->\n<!-- ocr-summary-run:10:2 --> Fix unsafe input","user":{"login":"github-actions[bot]"}}`
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
					return
				}
				_, _ = w.Write([]byte(strings.ReplaceAll(body, `:"head"`, `:"`+sha+`"`)))
			}))
			defer server.Close()
			rounds := 4
			if tc.changedHead {
				rounds = 2
			}
			for poll := range rounds {
				currentPoll = poll
				prReads = 0
				if poll == 2 && tc.retry {
					requestVisible = true
				}
				if poll == 1 && tc.failed {
					task["status"] = map[string]any{"phase": "Failed"}
				}
				if poll == 1 && len(tc.response) > 0 {
					task["status"] = map[string]any{"phase": "Succeeded", "results": map[string]any{"response": base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(tc.response, `:"head"`, `:"`+sha+`"`)))}}
				}

				client := github.NewClient(server.Client())
				client.BaseURL, _ = url.Parse(server.URL + "/")
				kube, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
				if err != nil {
					t.Fatal(err)
				}

				err = merge.Assess(context.Background(), client, review.NewCache(), kube, []string{"owner/repo"}, merge.AssessmentConfig{Namespace: "agents", Workspaces: map[string]string{"owner/repo": "repo", "other/repo": "other"}, AgentConfig: "editor", Secret: "codex"}, "controller")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
			if approved != (tc.state == "approve") {
				t.Fatalf("lgtm=%v for %s", approved, tc.state)
			}
			wantCreates := 1
			if tc.retry && !tc.denied && !tc.edited && !tc.stale {
				wantCreates = 2
			}
			if len(savedComments) != 1 || !strings.Contains(savedComments[0].GetBody(), `"state":"`+tc.state+`"`) {
				t.Fatalf("assessment status=%v want %s", savedComments, tc.state)
			}
			if creates != wantCreates {
				t.Fatalf("Task creates=%d want 1", creates)
			}
			spec := task["spec"].(map[string]any)
			worker, ok := spec["worker"].(map[string]any)
			if !ok {
				t.Fatal("Task must use spec.worker")
			}
			if worker["credentials"].(map[string]any)["type"] != "oauth" {
				t.Fatal("Task must use deployed API-key credentials")
			}
			if worker["workspaceRef"].(map[string]any)["name"] != "repo" {
				t.Fatal("Task must use the workspace of its repository")
			}
			if spec["branch"] != "agent-fix" {
				t.Fatal("Task lost PR branch")
			}
			if !strings.Contains(spec["prompt"].(string), sha) {
				t.Fatal("assessment lost head")
			}
		})
	}
}
