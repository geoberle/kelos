package editor_test

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
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/geoberle/kelos/internal/editor"
)

func TestCurrentFindingsCreateRetainedTask(t *testing.T) {
	for _, tc := range []struct {
		name              string
		inline, ambiguous bool
	}{
		{name: "summary finding"},
		{name: "inline finding", inline: true},
		{name: "ambiguous inline finding", inline: true, ambiguous: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := os.ReadFile("../review/testdata/complete.json")
			if err != nil {
				t.Fatal(err)
			}
			result = []byte(strings.ReplaceAll(string(result), `"comments":[]`, `"comments":[{"path":"main.go","start_line":1,"end_line":1,"content":"Fix unsafe input","severity":"high"}]`))
			archives := map[string][]byte{}
			for name, body := range map[string]string{
				"identity.json":    `{"repository":"owner/repo","pr":1,"run":10,"attempt":2,"head":"head","base":"base"}`,
				"ocr-result.json":  string(result),
				"publication.json": fmt.Sprintf(`{"repository":"owner/repo","pr":1,"run":10,"attempt":2,"head":"head","base":"base","summary":"https://github.com/owner/repo/issues/1#issuecomment-50","total":%d,"failed":%d}`, 1, 0),
			} {
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
			var task map[string]any
			creates := 0
			pr := func() string {
				return `{"number":1,"state":"open","head":{"sha":"head","ref":"agent-fix","repo":{"full_name":"owner/repo"}},"base":{"sha":"base"},"labels":[{"name":"agent-managed"}]}`
			}

			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks" {
					if r.Method == "POST" {
						creates++
						if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
							t.Error(err)
						}
						task["status"] = map[string]any{"phase": "Pending"}
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
				case "/repos/owner/repo/pulls":
					body = "[" + pr() + "]"
				case "/repos/owner/repo/pulls/1":
					body = pr()
				case "/repos/owner/repo/actions/workflows/ocr.yml":
					body = `{"id":8}`
				case "/repos/owner/repo/actions/workflows/8/runs":
					body = `{"workflow_runs":[{"id":10,"workflow_id":8,"path":".github/workflows/ocr.yml","event":"pull_request_target","run_attempt":2,"status":"completed","conclusion":"success","pull_requests":[{"number":1}]}]}`
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
					_, _ = w.Write(archives[strings.TrimPrefix(r.URL.Path, "/download/")])
					return
				case "/repos/owner/repo/compare/base...head":
					body = `{"merge_base_commit":{"sha":"base"}}`
				case "/repos/owner/repo/pulls/1/files":
					body = `[{"filename":"main.go"}]`
				case "/repos/owner/repo/issues/1/comments", "/repos/owner/repo/pulls/1/reviews":
					body = "[]"
				case "/repos/owner/repo/pulls/1/comments":
					body = "[]"
					if tc.inline {
						comment := `{"id":70,"path":"main.go","original_commit_id":"head","original_line":1,"html_url":"https://github.com/owner/repo/pull/1#discussion_r70","body":"<!-- ocr-10-2-0123456789abcdef --> Fix unsafe input","user":{"login":"github-actions[bot]"}}`
						body = "[" + comment + "]"
						if tc.ambiguous {
							body = "[" + comment + "," + strings.ReplaceAll(comment, `"id":70`, `"id":71`) + "]"
						}
					}

				case "/repos/owner/repo/issues/comments/50":
					body = `{"id":50,"body":"<!-- ocr-summary -->\n<!-- ocr-summary-run:10:2 --> Fix unsafe input","user":{"login":"github-actions[bot]"}}`
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			for range 2 {
				client := github.NewClient(server.Client())
				client.BaseURL, _ = url.Parse(server.URL + "/")
				kube, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
				if err != nil {
					t.Fatal(err)
				}
				err = editor.Poll(context.Background(), client, kube, []string{"owner/repo"}, editor.Config{Namespace: "agents", Workspace: "repo", AgentConfig: "editor", Secret: "codex"}, "controller")
				if (err != nil) != tc.ambiguous {
					t.Fatalf("unexpected error: %v", err)
				}
			}
			if tc.ambiguous {
				if creates != 0 {
					t.Fatal("ambiguous finding created Task")
				}
				return
			}
			if creates != 1 {
				t.Fatalf("Task creates=%d want 1", creates)
			}
			spec := task["spec"].(map[string]any)
			worker, ok := spec["worker"].(map[string]any)
			if !ok {
				t.Fatal("Task must use spec.worker")
			}
			if worker["credentials"].(map[string]any)["type"] != "api-key" {
				t.Fatal("Task must use deployed API-key credentials")
			}
			if spec["branch"] != "agent-fix" {
				t.Fatal("Task lost PR branch")
			}
			annotations := task["metadata"].(map[string]any)["annotations"].(map[string]any)
			var assignment editor.Assignment
			if err := json.Unmarshal([]byte(annotations["orchestrator.kelos.dev/assignment"].(string)), &assignment); err != nil {
				t.Fatal(err)
			}
			if len(assignment.Findings) != 1 || assignment.Findings[0].Content != "Fix unsafe input" {
				t.Fatalf("incorrect findings: %+v", assignment.Findings)
			}
			wantID := int64(0)
			if tc.inline {
				wantID = 70
			}
			if assignment.Findings[0].ReviewCommentID != wantID {
				t.Fatalf("wrong inline correlation: %+v", assignment.Findings[0])
			}
		})
	}
}
