package editor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v80/github"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/hearthctl/kelos-orchestrator/internal/editor"
	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

func TestDefectFoundByTheAssessmentStartsEditor(t *testing.T) {
	// The controller's assessment history: the newest assessment found a defect on the head.
	defect := `[{"id":7,"html_url":"https://github.com/owner/repo/pull/1#issuecomment-7","user":{"login":"controller"},"body":"<!-- kelos-assessment/v1 -->\n{\"attempts\":[{\"task\":\"assessment-a\",\"head\":\"head\",\"state\":\"needs-fix\",\"reason\":\"Removing a Slot deletes an Item on the wrong Page.\"}]}"}]`
	for _, tc := range []struct {
		name string
		// comments is GitHub's list of conversation comments on the pull request
		comments string
		// assigned are the assessment finding IDs of editor Tasks that already finished
		assigned []string
		// wantTask expects one editor Task for the defect
		wantTask bool
		// wantLimit expects the round limit to be reported instead of a Task
		wantLimit bool
	}{
		{
			name:     "defect on the head starts an editor",
			comments: defect,
			wantTask: true,
		},
		{
			name:     "pull request without an assessment starts nothing",
			comments: `[]`,
		},
		{
			name:     "defect that an editor already had starts nothing",
			comments: defect,
			assigned: []string{"assessment/owner/repo/1/assessment-a"},
		},
		{
			name:     "new defect after an earlier repair starts another editor",
			comments: defect,
			assigned: []string{"assessment/owner/repo/1/assessment-0"},
			wantTask: true,
		},
		{
			name:      "round limit stops further editors",
			comments:  defect,
			assigned:  []string{"assessment/owner/repo/1/assessment-0", "assessment/owner/repo/1/assessment-1"},
			wantLimit: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var created []map[string]any
			var posted []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "GET" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					items := []any{}
					for i, id := range tc.assigned {
						assignment, _ := json.Marshal(editor.Assignment{Repository: "owner/repo", PR: 1, Head: fmt.Sprintf("old-%d", i), Findings: []editor.Finding{{ID: id, Content: "defect"}}})
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
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/issues/1/comments":
					_, _ = w.Write([]byte(tc.comments))
				case r.Method == "POST" && r.URL.Path == "/repos/owner/repo/issues/1/comments":
					var comment struct {
						Body string `json:"body"`
					}
					if err := json.NewDecoder(r.Body).Decode(&comment); err != nil {
						t.Error(err)
					}
					posted = append(posted, comment.Body)
					_, _ = w.Write([]byte(`{}`))
				case r.Method == "GET" && (r.URL.Path == "/repos/owner/repo/pulls/1/comments" || r.URL.Path == "/repos/owner/repo/pulls/1/reviews"):
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/commits/head/check-runs":
					_, _ = w.Write([]byte(`{"total_count":0,"check_runs":[]}`))
				// Without a defect the editor looks for review evidence and finds none.
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
			client := github.NewClient(server.Client())
			client.BaseURL, _ = url.Parse(server.URL + "/")
			kube, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			err = editor.Poll(context.Background(), client, review.NewCache(), kube, []string{"owner/repo"}, editor.Config{Namespace: "agents", Workspaces: map[string]string{"owner/repo": "repo"}, AgentConfig: "editor", Secret: "codex"}, "controller")
			if tc.wantLimit {
				if err == nil || !strings.Contains(err.Error(), "round limit of 2 assessment repairs reached") {
					t.Fatalf("error=%v want the round limit", err)
				}
				if len(created) != 0 || len(posted) != 1 || !strings.HasPrefix(posted[0], "<!-- kelos-editor-failure/v1 assessment-round-limit -->") {
					t.Fatalf("created=%d posted=%q", len(created), posted)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(posted) != 0 {
				t.Fatalf("unexpected comments: %q", posted)
			}
			if !tc.wantTask {
				if len(created) != 0 {
					t.Fatalf("unexpected Tasks: %v", created)
				}
				return
			}
			if len(created) != 1 {
				t.Fatalf("Tasks=%d want 1", len(created))
			}
			var assignment editor.Assignment
			annotation := created[0]["metadata"].(map[string]any)["annotations"].(map[string]any)["orchestrator.kelos.dev/assignment"].(string)
			if err := json.Unmarshal([]byte(annotation), &assignment); err != nil {
				t.Fatal(err)
			}
			if assignment.Head != "head" || assignment.Run != 0 || len(assignment.Findings) != 1 {
				t.Fatalf("assignment=%+v", assignment)
			}
			finding := assignment.Findings[0]
			if finding.ID != "assessment/owner/repo/1/assessment-a" || !strings.HasSuffix(finding.Content, "Removing a Slot deletes an Item on the wrong Page.") || finding.Source != "https://github.com/owner/repo/pull/1#issuecomment-7" {
				t.Fatalf("finding=%+v", finding)
			}
		})
	}
}
