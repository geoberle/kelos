package admission_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/go-github/v80/github"

	"github.com/hearthctl/kelos-orchestrator/internal/admission"
)

func TestAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, labels, blockers, want string
		fail                         bool
	}{
		{name: "eligible", labels: `[{"name":"ready-for-agent"}]`, blockers: `[]`, want: "admit"},
		{name: "missing readiness", labels: `[]`, blockers: `[]`, want: "none"},
		{name: "open blocker", labels: `[{"name":"ready-for-agent"}]`, blockers: `[{"state":"open"}]`, want: "none"},
		{name: "fulfilled blocker", labels: `[{"name":"ready-for-agent"}]`, blockers: `[{"state":"closed"}]`, want: "admit"},
		{name: "withdraw", labels: `[{"name":"agent-start"}]`, blockers: `[]`, want: "withdraw"},
		{name: "already admitted", labels: `[{"name":"ready-for-agent"},{"name":"agent-start"}]`, blockers: `[]`, want: "none"},
		{name: "new blocker withdraws admission", labels: `[{"name":"ready-for-agent"},{"name":"agent-start"}]`, blockers: `[{"state":"open"}]`, want: "withdraw"},
		{name: "failure", labels: `[{"name":"ready-for-agent"}]`, blockers: `[]`, want: "none", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mutations []string
			labels := tc.labels
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !strings.HasPrefix(r.URL.Path, "/repos/owner/allowed/issues") {
					t.Errorf("outside allowlist: %s", r.URL)
					w.WriteHeader(500)
					return
				}
				switch {
				case r.Method == "POST":
					var body []string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body) != 1 || body[0] != "agent-start" {
						t.Errorf("unexpected labels: %v", body)
					}
					mutations = append(mutations, "POST agent-start")
					labels = `[{"name":"ready-for-agent"},{"name":"agent-start"}]`
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "DELETE":
					mutations = append(mutations, "DELETE agent-start")
					labels = `[]`
					w.WriteHeader(204)
				case strings.HasSuffix(r.URL.Path, "/dependencies/blocked_by"):
					if tc.fail {
						w.WriteHeader(503)
						return
					}
					_, _ = w.Write([]byte(tc.blockers))
				default:
					_, _ = w.Write([]byte(`[{"number":1,"state":"open","labels":` + labels + `}]`))
				}
			}))
			defer server.Close()
			client := github.NewClient(server.Client())
			client.BaseURL, _ = url.Parse(server.URL + "/")
			for range 2 {
				err := admission.Poll(context.Background(), client, []string{"owner/allowed"}, "")
				if (err != nil) != tc.fail {
					t.Fatalf("error: %v", err)
				}
			}
			golden, err := os.ReadFile("testdata/" + tc.want + ".golden")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(mutations, "\n"); got != strings.TrimSpace(string(golden)) {
				t.Fatalf("mutations %q want %q", got, golden)
			}
		})
	}
}

func TestAdmissionPostsRunCommand(t *testing.T) {
	for _, tc := range []struct {
		name       string
		labels     string
		comments   string
		controller string
		want       string
	}{
		{
			name:       "admitting posts the command before the label",
			labels:     `[{"name":"ready-for-agent"}]`,
			comments:   `[]`,
			controller: "controller[bot]",
			want:       "command-and-admit",
		},
		{
			name:       "command is never posted twice on an issue",
			labels:     `[{"name":"ready-for-agent"}]`,
			comments:   `[{"user":{"login":"controller[bot]"},"body":"/kelos run"}]`,
			controller: "controller[bot]",
			want:       "admit",
		},
		{
			name:       "command from another user does not count as posted",
			labels:     `[{"name":"ready-for-agent"}]`,
			comments:   `[{"user":{"login":"someone"},"body":"/kelos run"}]`,
			controller: "controller[bot]",
			want:       "command-and-admit",
		},
		{
			name:       "already admitted issue gets no command",
			labels:     `[{"name":"ready-for-agent"},{"name":"agent-start"}]`,
			comments:   `[]`,
			controller: "controller[bot]",
			want:       "none",
		},
		{
			name:       "no controller identity admits by label only",
			labels:     `[{"name":"ready-for-agent"}]`,
			comments:   `[]`,
			controller: "",
			want:       "admit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mutations []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "POST" && r.URL.Path == "/repos/owner/allowed/issues/1/comments":
					var comment struct {
						Body string `json:"body"`
					}
					if err := json.NewDecoder(r.Body).Decode(&comment); err != nil {
						t.Error(err)
					}
					mutations = append(mutations, "POST comment "+comment.Body)
					_, _ = w.Write([]byte(`{}`))
				case r.Method == "POST" && r.URL.Path == "/repos/owner/allowed/issues/1/labels":
					mutations = append(mutations, "POST agent-start")
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/allowed/issues/1/comments":
					if len(tc.controller) == 0 {
						t.Error("comments read without a controller identity")
					}
					_, _ = w.Write([]byte(tc.comments))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/allowed/issues/1/dependencies/blocked_by":
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/allowed/issues":
					_, _ = w.Write([]byte(`[{"number":1,"state":"open","labels":` + tc.labels + `}]`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client := github.NewClient(server.Client())
			client.BaseURL, _ = url.Parse(server.URL + "/")
			if err := admission.Poll(context.Background(), client, []string{"owner/allowed"}, tc.controller); err != nil {
				t.Fatal(err)
			}
			golden, err := os.ReadFile("testdata/" + tc.want + ".golden")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(mutations, "\n"); got != strings.TrimSpace(string(golden)) {
				t.Fatalf("mutations %q want %q", got, golden)
			}
		})
	}
}
