package editor_test

import (
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

	"github.com/hearthctl/kelos-orchestrator/internal/editor"
	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

func TestFailedCheckStartsEditor(t *testing.T) {
	for _, tc := range []struct {
		name string
		// checkRuns is GitHub's answer for the verify check on the head
		checkRuns string
		// assigned are the check finding IDs of editor Tasks that already finished
		assigned []string
		// wantFinding is the golden file holding the created Task's finding; empty when no Task is expected
		wantFinding string
		// wantLimit expects the round limit to be reported instead of a Task
		wantLimit bool
	}{
		{
			name:        "failed check starts an editor with the end of the log",
			checkRuns:   `{"total_count":1,"check_runs":[{"id":55,"name":"verify","status":"completed","conclusion":"failure","html_url":"https://github.com/owner/repo/actions/runs/9/job/55","app":{"slug":"github-actions"}}]}`,
			wantFinding: "failed-check.golden",
		},
		{
			name:      "passed check starts nothing",
			checkRuns: `{"total_count":1,"check_runs":[{"id":55,"name":"verify","status":"completed","conclusion":"success","app":{"slug":"github-actions"}}]}`,
		},
		{
			name:      "running check starts nothing",
			checkRuns: `{"total_count":1,"check_runs":[{"id":55,"name":"verify","status":"in_progress","app":{"slug":"github-actions"}}]}`,
		},
		{
			name:      "no check starts nothing",
			checkRuns: `{"total_count":0,"check_runs":[]}`,
		},
		{
			name:      "failed check of another app starts nothing",
			checkRuns: `{"total_count":1,"check_runs":[{"id":55,"name":"verify","status":"completed","conclusion":"failure","app":{"slug":"someone-else"}}]}`,
		},
		{
			name:      "failed check that an editor already had starts nothing",
			checkRuns: `{"total_count":1,"check_runs":[{"id":55,"name":"verify","status":"completed","conclusion":"failure","app":{"slug":"github-actions"}}]}`,
			assigned:  []string{"check/owner/repo/1/55"},
		},
		{
			name:        "new failure after an earlier repair starts another editor",
			checkRuns:   `{"total_count":1,"check_runs":[{"id":55,"name":"verify","status":"completed","conclusion":"failure","html_url":"https://github.com/owner/repo/actions/runs/9/job/55","app":{"slug":"github-actions"}}]}`,
			assigned:    []string{"check/owner/repo/1/40"},
			wantFinding: "failed-check.golden",
		},
		{
			name:      "round limit stops further editors",
			checkRuns: `{"total_count":1,"check_runs":[{"id":55,"name":"verify","status":"completed","conclusion":"failure","app":{"slug":"github-actions"}}]}`,
			assigned:  []string{"check/owner/repo/1/40", "check/owner/repo/1/41", "check/owner/repo/1/42"},
			wantLimit: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var created []map[string]any
			var comments []string
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "GET" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					items := []any{}
					for i, id := range tc.assigned {
						assignment, _ := json.Marshal(editor.Assignment{Repository: "owner/repo", PR: 1, Head: fmt.Sprintf("old-%d", i), Findings: []editor.Finding{{ID: id, Content: "failed"}}})
						items = append(items, map[string]any{
							"apiVersion": "kelos.dev/v1alpha2", "kind": "Task",
							"metadata": map[string]any{"name": fmt.Sprintf("editor-old-%d", i), "namespace": "agents", "annotations": map[string]any{"orchestrator.kelos.dev/assignment": string(assignment), "orchestrator.kelos.dev/result-recorded": "true"}},
							"status":   map[string]any{"phase": "Succeeded"},
						})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "kelos.dev/v1alpha2", "kind": "TaskList", "items": items})
				case r.Method == "POST" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					var task map[string]any
					if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
						t.Error(err)
					}
					created = append(created, task)
					_ = json.NewEncoder(w).Encode(task)
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/pulls":
					_, _ = w.Write([]byte(`[{"number":1,"labels":[{"name":"agent-managed"}],"head":{"sha":"head","ref":"agent/issue-3","repo":{"full_name":"owner/repo"}}}]`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/pulls/1":
					_, _ = w.Write([]byte(`{"number":1,"head":{"sha":"head","ref":"agent/issue-3","repo":{"full_name":"owner/repo"}}}`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/commits/head/check-runs":
					if r.URL.Query().Get("check_name") != "verify" || r.URL.Query().Get("filter") != "latest" {
						t.Errorf("unexpected check query: %s", r.URL.RawQuery)
					}
					_, _ = w.Write([]byte(tc.checkRuns))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/actions/jobs/55/logs":
					w.Header().Set("Location", server.URL+"/signed-log")
					w.WriteHeader(http.StatusFound)
				case r.Method == "GET" && r.URL.Path == "/signed-log":
					// The log is on another host and must not see GitHub credentials.
					if len(r.Header.Get("Authorization")) > 0 {
						t.Error("log download carried credentials")
					}
					log, err := os.ReadFile("testdata/job.log")
					if err != nil {
						t.Error(err)
					}
					_, _ = w.Write(log)
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/issues/1/comments":
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "POST" && r.URL.Path == "/repos/owner/repo/issues/1/comments":
					var comment struct {
						Body string `json:"body"`
					}
					if err := json.NewDecoder(r.Body).Decode(&comment); err != nil {
						t.Error(err)
					}
					comments = append(comments, comment.Body)
					_, _ = w.Write([]byte(`{}`))
				case r.Method == "GET" && (r.URL.Path == "/repos/owner/repo/pulls/1/comments" || r.URL.Path == "/repos/owner/repo/pulls/1/reviews"):
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/collaborators/geoberle/permission":
					_, _ = w.Write([]byte(`{"role_name":"admin"}`))
				// Without a failed check the editor looks for review evidence and finds none.
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/actions/workflows/ocr.yml":
					_, _ = w.Write([]byte(`{"id":8}`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/actions/workflows/8/runs":
					_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client := github.NewClient(server.Client()).WithAuthToken("secret")
			client.BaseURL, _ = url.Parse(server.URL + "/")
			kube, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			err = editor.Poll(context.Background(), client, review.NewCache(), kube, []string{"owner/repo"}, editor.Config{Namespace: "agents", Workspaces: map[string]string{"owner/repo": "repo"}, AgentConfig: "editor", Secret: "codex"}, "controller")
			if tc.wantLimit {
				if err == nil || !strings.Contains(err.Error(), "round limit of 3 check repairs reached") {
					t.Fatalf("error=%v want the round limit", err)
				}
				if len(created) != 0 || len(comments) != 1 || !strings.HasPrefix(comments[0], "<!-- kelos-editor-failure/v1 check-round-limit -->") {
					t.Fatalf("created=%d comments=%q", len(created), comments)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(comments) != 0 {
				t.Fatalf("unexpected comments: %q", comments)
			}
			if len(tc.wantFinding) == 0 {
				if len(created) != 0 {
					t.Fatalf("unexpected Tasks: %v", created)
				}
				return
			}
			if len(created) != 1 {
				t.Fatalf("Tasks=%d want 1", len(created))
			}
			spec := created[0]["spec"].(map[string]any)
			if spec["branch"] != "agent/issue-3" {
				t.Fatalf("branch=%v", spec["branch"])
			}
			var assignment editor.Assignment
			annotation := created[0]["metadata"].(map[string]any)["annotations"].(map[string]any)["orchestrator.kelos.dev/assignment"].(string)
			if err := json.Unmarshal([]byte(annotation), &assignment); err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(spec["prompt"].(string), annotation) {
				t.Fatal("prompt does not end with the assignment")
			}
			if assignment.Repository != "owner/repo" || assignment.PR != 1 || assignment.Head != "head" || assignment.Run != 0 || len(assignment.Findings) != 1 {
				t.Fatalf("assignment=%+v", assignment)
			}
			finding := assignment.Findings[0]
			got := finding.ID + "\n" + finding.Source + "\n" + finding.Content
			want, err := os.ReadFile("testdata/" + tc.wantFinding)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("finding:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
