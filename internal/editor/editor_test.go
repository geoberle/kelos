package editor_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
