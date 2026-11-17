package feedback_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-github/v80/github"

	"github.com/hearthctl/kelos-orchestrator/internal/feedback"
)

func TestLatestWordWins(t *testing.T) {
	// The controller's record of a request it observed at 10:01; the request
	// itself carries no time, as in records written before requests had one.
	observed := `{"id":80,"created_at":"2026-11-17T10:01:00Z","updated_at":"2026-11-17T10:01:00Z","user":{"login":"controller"},"body":"<!-- kelos-feedback/v1 -->\n[{\"id\":\"human/comment/1/abc\",\"content\":\"Fix the race\",\"path\":\"\",\"source\":\"https://github.com/acme/repo/pull/1#issuecomment-1\"}]"}`
	for _, tc := range []struct {
		name string
		// comments is GitHub's list of conversation comments, oldest first
		comments string
		// wantPending are the contents of the requests that stay pending
		wantPending []string
		// wantNotice expects one comment that names the dismissed requests
		wantNotice bool
	}{
		{
			name: "request without an approval stays pending",
			comments: `[
				{"id":1,"created_at":"2026-11-17T10:00:00Z","updated_at":"2026-11-17T10:00:00Z","user":{"login":"geoberle"},"body":"Fix the race","html_url":"https://github.com/acme/repo/pull/1#issuecomment-1"}]`,
			wantPending: []string{"Fix the race"},
		},
		{
			name: "later approval dismisses the request",
			comments: `[
				{"id":1,"created_at":"2026-11-17T10:00:00Z","updated_at":"2026-11-17T10:00:00Z","user":{"login":"geoberle"},"body":"Fix the race","html_url":"https://github.com/acme/repo/pull/1#issuecomment-1"},
				{"id":2,"created_at":"2026-11-17T11:00:00Z","updated_at":"2026-11-17T11:00:00Z","user":{"login":"geoberle"},"body":"/lgtm\nacceptable","html_url":"https://github.com/acme/repo/pull/1#issuecomment-2"}]`,
			wantNotice: true,
		},
		{
			name: "request after the approval stays pending",
			comments: `[
				{"id":2,"created_at":"2026-11-17T09:00:00Z","updated_at":"2026-11-17T09:00:00Z","user":{"login":"geoberle"},"body":"/lgtm","html_url":"https://github.com/acme/repo/pull/1#issuecomment-2"},
				{"id":1,"created_at":"2026-11-17T10:00:00Z","updated_at":"2026-11-17T10:00:00Z","user":{"login":"geoberle"},"body":"Fix the race","html_url":"https://github.com/acme/repo/pull/1#issuecomment-1"}]`,
			wantPending: []string{"Fix the race"},
		},
		{
			name: "request edited after the approval stays pending",
			comments: `[
				{"id":1,"created_at":"2026-11-17T10:00:00Z","updated_at":"2026-11-17T12:00:00Z","user":{"login":"geoberle"},"body":"Fix the race","html_url":"https://github.com/acme/repo/pull/1#issuecomment-1"},
				{"id":2,"created_at":"2026-11-17T11:00:00Z","updated_at":"2026-11-17T11:00:00Z","user":{"login":"geoberle"},"body":"/lgtm","html_url":"https://github.com/acme/repo/pull/1#issuecomment-2"}]`,
			wantPending: []string{"Fix the race"},
		},
		{
			name: "only the requests before the approval are dismissed",
			comments: `[
				{"id":1,"created_at":"2026-11-17T10:00:00Z","updated_at":"2026-11-17T10:00:00Z","user":{"login":"geoberle"},"body":"Fix the race","html_url":"https://github.com/acme/repo/pull/1#issuecomment-1"},
				{"id":2,"created_at":"2026-11-17T11:00:00Z","updated_at":"2026-11-17T11:00:00Z","user":{"login":"geoberle"},"body":"/lgtm","html_url":"https://github.com/acme/repo/pull/1#issuecomment-2"},
				{"id":3,"created_at":"2026-11-17T12:00:00Z","updated_at":"2026-11-17T12:00:00Z","user":{"login":"geoberle"},"body":"Rename the flag","html_url":"https://github.com/acme/repo/pull/1#issuecomment-3"}]`,
			wantPending: []string{"Rename the flag"},
			wantNotice:  true,
		},
		{
			name: "recorded request whose comment is gone is dismissed by a later approval",
			comments: `[` + observed + `,
				{"id":2,"created_at":"2026-11-17T11:00:00Z","updated_at":"2026-11-17T11:00:00Z","user":{"login":"geoberle"},"body":"/lgtm","html_url":"https://github.com/acme/repo/pull/1#issuecomment-2"}]`,
			wantNotice: true,
		},
		{
			name: "recorded request whose comment is gone outlives an earlier approval",
			comments: `[
				{"id":2,"created_at":"2026-11-17T09:00:00Z","updated_at":"2026-11-17T09:00:00Z","user":{"login":"geoberle"},"body":"/lgtm","html_url":"https://github.com/acme/repo/pull/1#issuecomment-2"},` + observed + `]`,
			wantPending: []string{"Fix the race"},
		},
		{
			name: "Prow's withdrawal dismisses nothing",
			comments: `[
				{"id":1,"created_at":"2026-11-17T10:00:00Z","updated_at":"2026-11-17T10:00:00Z","user":{"login":"geoberle"},"body":"Fix the race","html_url":"https://github.com/acme/repo/pull/1#issuecomment-1"},
				{"id":2,"created_at":"2026-11-17T11:00:00Z","updated_at":"2026-11-17T11:00:00Z","user":{"login":"geoberle"},"body":"/lgtm cancel","html_url":"https://github.com/acme/repo/pull/1#issuecomment-2"}]`,
			wantPending: []string{"Fix the race"},
		},
		{
			name: "edited approval dismisses nothing",
			comments: `[
				{"id":1,"created_at":"2026-11-17T10:00:00Z","updated_at":"2026-11-17T10:00:00Z","user":{"login":"geoberle"},"body":"Fix the race","html_url":"https://github.com/acme/repo/pull/1#issuecomment-1"},
				{"id":2,"created_at":"2026-11-17T09:00:00Z","updated_at":"2026-11-17T11:00:00Z","user":{"login":"geoberle"},"body":"/lgtm","html_url":"https://github.com/acme/repo/pull/1#issuecomment-2"}]`,
			wantPending: []string{"Fix the race"},
		},
		{
			name: "approval by someone else dismisses nothing",
			comments: `[
				{"id":1,"created_at":"2026-11-17T10:00:00Z","updated_at":"2026-11-17T10:00:00Z","user":{"login":"geoberle"},"body":"Fix the race","html_url":"https://github.com/acme/repo/pull/1#issuecomment-1"},
				{"id":2,"created_at":"2026-11-17T11:00:00Z","updated_at":"2026-11-17T11:00:00Z","user":{"login":"someone"},"body":"/lgtm","html_url":"https://github.com/acme/repo/pull/1#issuecomment-2"}]`,
			wantPending: []string{"Fix the race"},
		},
		{
			name: "run command on a pull request is not a request",
			comments: `[
				{"id":1,"created_at":"2026-11-17T10:00:00Z","updated_at":"2026-11-17T10:00:00Z","user":{"login":"geoberle"},"body":"/kelos run","html_url":"https://github.com/acme/repo/pull/1#issuecomment-1"}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
						c.CreatedAt = &github.Timestamp{}
						saved = append(saved, &c)
						_ = json.NewEncoder(w).Encode(c)
						return
					}
					var comments []*github.IssueComment
					if err := json.Unmarshal([]byte(tc.comments), &comments); err != nil {
						t.Error(err)
					}
					_ = json.NewEncoder(w).Encode(append(comments, saved...))
				case "/repos/acme/repo/pulls/1/comments", "/repos/acme/repo/pulls/1/reviews":
					_, _ = w.Write([]byte(`[]`))
				case "/repos/acme/repo/collaborators/geoberle/permission":
					_, _ = w.Write([]byte(`{"role_name":"admin"}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			// A second poll must give the same answer and write nothing more.
			for range 2 {
				c := github.NewClient(server.Client())
				c.BaseURL, _ = c.BaseURL.Parse(server.URL + "/")
				pending, err := feedback.Pending(context.Background(), c, "acme", "repo", 1, "head", "controller")
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, f := range pending {
					got = append(got, f.Content)
				}
				if strings.Join(got, "|") != strings.Join(tc.wantPending, "|") {
					t.Fatalf("pending=%q want %q", got, tc.wantPending)
				}
			}
			notices := 0
			for _, c := range saved {
				if strings.HasPrefix(c.GetBody(), "<!-- kelos-dismissed-feedback/v1 2 -->") {
					notices++
					if !strings.Contains(c.GetBody(), "https://github.com/acme/repo/pull/1#issuecomment-1") || !strings.Contains(c.GetBody(), "https://github.com/acme/repo/pull/1#issuecomment-2") {
						t.Fatalf("notice names neither request nor approval:\n%s", c.GetBody())
					}
				}
			}
			want := 0
			if tc.wantNotice {
				want = 1
			}
			if notices != want {
				t.Fatalf("notices=%d want %d: %v", notices, want, saved)
			}
		})
	}
}
