package editor_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v80/github"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/hearthctl/kelos-orchestrator/internal/editor"
	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

// A Task that worked on a human request blocks its pull request while the
// request is pending. Once the request is gone, dismissed by a later approval
// or deleted with its record, the Task has nothing left to answer.
func TestTaskOfARequestThatIsGoneNoLongerBlocks(t *testing.T) {
	for _, tc := range []struct {
		name string
		// phase and response describe the finished editor Task
		phase    string
		response string
	}{
		{
			name:     "task that asked for a human",
			phase:    "Succeeded",
			response: `{"head":"head","commit":"head","dispositions":[{"id":"human/comment/1/abc","outcome":"needs-human","explanation":"No correction named."}]}`,
		},
		{
			name:  "task that failed",
			phase: "Failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorded := false
			patches := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "GET" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					assignment, _ := json.Marshal(editor.Assignment{Repository: "owner/repo", PR: 1, Head: "head", Findings: []editor.Finding{{ID: "human/comment/1/abc", Content: "/kelos run"}}})
					annotations := map[string]any{"orchestrator.kelos.dev/assignment": string(assignment)}
					if recorded {
						annotations["orchestrator.kelos.dev/result-recorded"] = "true"
					}
					status := map[string]any{"phase": tc.phase}
					if len(tc.response) > 0 {
						status["results"] = map[string]any{"response": base64.StdEncoding.EncodeToString([]byte(tc.response))}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "kelos.dev/v1alpha2", "kind": "TaskList", "items": []any{map[string]any{
						"apiVersion": "kelos.dev/v1alpha2", "kind": "Task",
						"metadata": map[string]any{"name": "editor-old", "namespace": "agents", "annotations": annotations},
						"status":   status,
					}}})
				case r.Method == "PATCH" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks/editor-old":
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{"metadata":{"annotations":{"orchestrator.kelos.dev/result-recorded":"true"}}}` {
						t.Errorf("unexpected patch: %s", body)
					}
					patches++
					recorded = true
					_, _ = w.Write([]byte(`{"apiVersion":"kelos.dev/v1alpha2","kind":"Task","metadata":{"name":"editor-old","namespace":"agents"}}`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/pulls":
					_, _ = w.Write([]byte(`[{"number":1,"labels":[{"name":"agent-managed"}],"head":{"sha":"head","ref":"agent/issue-3","repo":{"full_name":"owner/repo"}}}]`))
				// No request is pending: the pull request has no human comment and no record of one.
				case r.Method == "GET" && (r.URL.Path == "/repos/owner/repo/issues/1/comments" || r.URL.Path == "/repos/owner/repo/pulls/1/comments" || r.URL.Path == "/repos/owner/repo/pulls/1/reviews"):
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/commits/head/check-runs":
					_, _ = w.Write([]byte(`{"total_count":0,"check_runs":[]}`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/actions/workflows/ocr.yml":
					_, _ = w.Write([]byte(`{"id":8}`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/actions/workflows/8/runs":
					_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
				default:
					// In particular no comment is posted and no Task is created.
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
			// A second poll must find the Task already settled.
			for range 2 {
				if err := editor.Poll(context.Background(), client, review.NewCache(), kube, []string{"owner/repo"}, editor.Config{Namespace: "agents", Workspaces: map[string]string{"owner/repo": "repo"}, AgentConfig: "editor", Secret: "codex"}, "controller"); err != nil {
					t.Fatal(err)
				}
			}
			if patches != 1 {
				t.Fatalf("patches=%d want 1", patches)
			}
		})
	}
}
