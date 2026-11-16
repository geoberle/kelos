package review_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/google/go-github/v80/github"

	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

func TestPollEmptyRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/repos/owner/repo/pulls" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()
	client := github.NewClient(server.Client())
	client.BaseURL, _ = url.Parse(server.URL + "/")
	if err := review.Poll(context.Background(), client, []string{"owner/repo"}, "controller"); err != nil {
		t.Fatal(err)
	}
}

func TestMissingEvidenceWithdrawsCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, conclusion string
		failComment      bool
	}{
		{"missing artifact", "success", false}, {"failed publication", "failure", false}, {"status publication failure", "success", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mutations []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" {
					mutations = append(mutations, r.Method+" "+r.URL.Path)
					if r.Method == "DELETE" {
						w.WriteHeader(204)
						return
					}
					if tc.failComment {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					_, _ = w.Write([]byte("{}"))
					return
				}
				var body string
				switch r.URL.Path {
				case "/repos/owner/repo/pulls":
					body = `[{"number":1,"head":{"sha":"head"},"labels":[{"name":"agent-managed"},{"name":"review-done"}]}]`
				case "/repos/owner/repo/actions/workflows/ocr.yml":
					body = `{"id":8}`
				case "/repos/owner/repo/actions/workflows/8/runs":
					body = `{"workflow_runs":[{"id":10,"workflow_id":8,"path":".github/workflows/ocr.yml","event":"pull_request_target","run_attempt":2,"status":"completed","conclusion":"` + tc.conclusion + `"}]}`
				case "/repos/owner/repo/actions/runs/10/artifacts":
					body = `{"artifacts":[]}`
				case "/users/controller":
					body = `{"id":90,"login":"controller"}`
				case "/repos/owner/repo/issues/1/comments", "/repos/owner/repo/pulls/1/comments", "/repos/owner/repo/pulls/1/reviews":
					body = "[]"
				default:
					t.Errorf("unexpected GET %s", r.URL)
					http.Error(w, "unexpected", 500)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client := github.NewClient(server.Client())
			client.BaseURL, _ = url.Parse(server.URL + "/")
			if err := review.Poll(context.Background(), client, []string{"owner/repo"}, "controller"); (err != nil) != tc.failComment {
				t.Fatalf("poll error: %v", err)
			}
			want := []string{"DELETE /repos/owner/repo/issues/1/labels/review-done", "POST /repos/owner/repo/issues/1/comments"}
			if !reflect.DeepEqual(mutations, want) {
				t.Fatalf("mutations: %v, want %v", mutations, want)
			}
		})
	}
}
