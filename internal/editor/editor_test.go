package editor_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/go-github/v80/github"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/geoberle/kelos/internal/editor"
)

func TestUnenrolledPullRequestsCreateNoTasks(t *testing.T) {
	for _, body := range []string{`[]`, `[{"number":1,"state":"open","head":{"sha":"abc"}}]`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/acme/repo/pulls" {
					t.Errorf("unexpected API mutation/request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			gh := github.NewClient(server.Client())
			gh.BaseURL, _ = gh.BaseURL.Parse(server.URL + "/")
			kube, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if err := editor.Poll(context.Background(), gh, kube, []string{"acme/repo"}, editor.Config{Namespace: "agents", Workspace: "repo", AgentConfig: "editor", Secret: "codex"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRetainedEditorsPreventNewWork(t *testing.T) {
	for _, phase := range []string{"Pending", "Running", "Failed", "Waiting", "Succeeded"} {
		t.Run(phase, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/repos/acme/repo/pulls":
					_, _ = w.Write([]byte(`[{"number":1,"state":"open","labels":[{"name":"agent-managed"}],"head":{"sha":"abc"}}]`))
				case "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					if r.Method != "GET" {
						t.Errorf("unexpected Task mutation %s", r.Method)
					}
					_, _ = w.Write([]byte(`{"apiVersion":"kelos.dev/v1alpha2","kind":"TaskList","items":[{"metadata":{"name":"retained","annotations":{"orchestrator.kelos.dev/assignment":"{}"}},"status":{"phase":"` + phase + `"}}]}`))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			gh := github.NewClient(server.Client())
			gh.BaseURL, _ = gh.BaseURL.Parse(server.URL + "/")
			kube, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				err = editor.Poll(context.Background(), gh, kube, []string{"acme/repo"}, editor.Config{Namespace: "agents", Workspace: "repo", AgentConfig: "editor", Secret: "codex"})
				if (err != nil) != (phase == "Succeeded") {
					t.Fatalf("unexpected result: %v", err)
				}
			}
		})
	}
}

func TestEditorResultAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, outcome    string
		missing, badHead bool
		wantError        bool
	}{
		{name: "fixed", outcome: "fixed"}, {name: "already satisfied", outcome: "already-satisfied"},
		{name: "human attention", outcome: "needs-human", wantError: true},
		{name: "missing disposition", missing: true, wantError: true},
		{name: "wrong head", outcome: "fixed", badHead: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assignment := `{"repository":"acme/repo","pr":1,"head":"abc","findings":[{"id":"finding-0","source":"https://github.com/acme/repo/pull/1#issuecomment-10"}]}`
			head := "abc"
			if tc.badHead {
				head = "wrong"
			}
			result := `{"head":"` + head + `","commit":"def","dispositions":[{"id":"finding-0","outcome":"` + tc.outcome + `","explanation":"Checked the assigned scenario."}]}`
			if tc.missing {
				result = `{"head":"abc","commit":"def","dispositions":[]}`
			}
			var replies []map[string]string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/repos/acme/repo/pulls":
					_, _ = w.Write([]byte(`[{"number":1,"labels":[{"name":"agent-managed"}],"head":{"sha":"abc"}}]`))
				case "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					body := map[string]any{"apiVersion": "kelos.dev/v1alpha2", "kind": "TaskList", "items": []any{map[string]any{"metadata": map[string]any{"name": "retained", "annotations": map[string]string{"orchestrator.kelos.dev/assignment": assignment}}, "status": map[string]any{"phase": "Succeeded", "results": map[string]string{"editor-result": result}}}}}
					_ = json.NewEncoder(w).Encode(body)
				case "/repos/acme/repo/compare/abc...def":
					_, _ = w.Write([]byte(`{"status":"ahead"}`))
				case "/repos/acme/repo/issues/1/comments":
					if r.Method == "POST" {
						var reply map[string]string
						_ = json.NewDecoder(r.Body).Decode(&reply)
						replies = append(replies, reply)
						_ = json.NewEncoder(w).Encode(reply)
					} else {
						_ = json.NewEncoder(w).Encode(replies)
					}
				case "/repos/acme/repo/actions/workflows/ocr.yml":
					_, _ = w.Write([]byte(`{"id":8}`))
				case "/repos/acme/repo/actions/workflows/8/runs":
					_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
				default:
					t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			gh := github.NewClient(server.Client())
			gh.BaseURL, _ = gh.BaseURL.Parse(server.URL + "/")
			kube, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				err = editor.Poll(context.Background(), gh, kube, []string{"acme/repo"}, editor.Config{Namespace: "agents"})
				if (err != nil) != tc.wantError {
					t.Fatalf("unexpected error %v", err)
				}
			}
			want := 1
			if tc.missing || tc.badHead {
				want = 0
			}
			if len(replies) != want {
				t.Fatalf("got %d replies, want %d", len(replies), want)
			}
			if tc.name == "fixed" {
				expected, err := os.ReadFile("testdata/fixed.golden")
				if err != nil {
					t.Fatal(err)
				}
				if replies[0]["body"] != string(expected) {
					t.Fatalf("reply differs from golden: %q", replies[0]["body"])
				}
			}
			if want == 1 && !strings.Contains(replies[0]["body"], "[finding-0](https://github.com/acme/repo/pull/1#issuecomment-10): **"+tc.outcome+"**") {
				t.Fatalf("missing correlation: %v", replies)
			}
		})
	}
}
