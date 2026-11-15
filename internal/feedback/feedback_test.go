package feedback_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v80/github"

	"github.com/geoberle/kelos/internal/feedback"
)

func TestObservedFeedbackSurvivesDeletion(t *testing.T) {
	for _, tc := range []struct {
		name               string
		deleted            bool
		handled            bool
		rewritten, spoofed bool
	}{
		{name: "retained source"}, {name: "deleted source", deleted: true}, {name: "handled source", handled: true},
		{name: "handled commit no longer on branch", handled: true, rewritten: true},
		{name: "copied receipt is not controller evidence", handled: true, spoofed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := true
			var saved []*github.IssueComment
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/repos/acme/repo/issues/1/comments":
					if r.Method == "POST" {
						var c github.IssueComment
						if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
							t.Error(err)
						}
						c.ID = github.Ptr(int64(90 + len(saved)))
						c.User = &github.User{Login: github.Ptr("controller")}
						saved = append(saved, &c)
						_ = json.NewEncoder(w).Encode(c)
						return
					}
					comments := append([]*github.IssueComment{}, saved...)
					if source {
						comments = append(comments, &github.IssueComment{ID: github.Ptr(int64(1)), Body: github.Ptr("Fix the race"), HTMLURL: github.Ptr("https://github.com/acme/repo/pull/1#issuecomment-1"), User: &github.User{Login: github.Ptr("geoberle")}})
					}
					_ = json.NewEncoder(w).Encode(comments)
				case "/repos/acme/repo/pulls/1/comments", "/repos/acme/repo/pulls/1/reviews":
					_, _ = w.Write([]byte(`[]`))
				case "/repos/acme/repo/compare/head...new":
					_, _ = w.Write([]byte(`{"status":"diverged"}`))
				case "/repos/acme/repo/collaborators/geoberle/permission":
					_, _ = w.Write([]byte(`{"role_name":"admin"}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			for poll := range 2 {
				if poll == 1 && tc.deleted {
					source = false
				}
				c := github.NewClient(server.Client())
				c.BaseURL, _ = c.BaseURL.Parse(server.URL + "/")
				head := "head"
				if poll == 1 && tc.rewritten {
					head = "new"
				}
				if poll == 1 && tc.spoofed {
					saved[len(saved)-1].User.Login = github.Ptr("someone-else")
				}
				pending, err := feedback.Pending(context.Background(), c, "acme", "repo", 1, head, "controller")
				if err != nil {
					t.Fatal(err)
				}
				want := 1
				if poll == 1 && tc.handled && !tc.rewritten && !tc.spoofed {
					want = 0
				}
				if len(pending) != want || (want == 1 && pending[0].Content != "Fix the race") {
					t.Fatalf("lost pending feedback: %+v", pending)
				}
				if poll == 0 && tc.handled {
					if err := feedback.Record(context.Background(), c, "acme", "repo", 1, "head", "controller", "editor-1", []string{pending[0].ID}); err != nil {
						t.Fatal(err)
					}
				}
			}
			wantSaved := 1
			if tc.handled {
				wantSaved = 2
			}
			if len(saved) != wantSaved {
				t.Fatalf("snapshots=%d want 1", len(saved))
			}
		})
	}
}
