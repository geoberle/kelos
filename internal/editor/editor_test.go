package editor_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/go-github/v80/github"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/hearthctl/kelos-orchestrator/internal/editor"
	"github.com/hearthctl/kelos-orchestrator/internal/review"
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
			if err := editor.Poll(context.Background(), gh, review.NewCache(), kube, []string{"acme/repo"}, editor.Config{Namespace: "agents", Workspaces: map[string]string{"acme/repo": "repo"}, AgentConfig: "editor", Secret: "codex"}, "controller"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRetainedEditorsPreventNewWork(t *testing.T) {
	for _, phase := range []string{"Pending", "Running", "Failed", "Waiting", "Succeeded"} {
		t.Run(phase, func(t *testing.T) {
			var statuses []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/repos/acme/repo/pulls":
					_, _ = w.Write([]byte(`[{"number":1,"state":"open","labels":[{"name":"agent-managed"}],"head":{"sha":"abc"}}]`))
				case "/repos/acme/repo/issues/1/comments":
					if r.Method == "POST" {
						var status map[string]any
						_ = json.NewDecoder(r.Body).Decode(&status)
						status["user"] = map[string]string{"login": "controller"}
						statuses = append(statuses, status)
						_ = json.NewEncoder(w).Encode(status)
					} else {
						_ = json.NewEncoder(w).Encode(statuses)
					}
				case "/repos/acme/repo/pulls/1/comments", "/repos/acme/repo/pulls/1/reviews":
					_, _ = w.Write([]byte(`[]`))
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
				err = editor.Poll(context.Background(), gh, review.NewCache(), kube, []string{"acme/repo"}, editor.Config{Namespace: "agents", Workspaces: map[string]string{"acme/repo": "repo"}, AgentConfig: "editor", Secret: "codex"}, "controller")
				if (err != nil) != (phase == "Succeeded" || phase == "Failed") {
					t.Fatalf("unexpected result: %v", err)
				}
			}
			if phase == "Failed" || phase == "Succeeded" {
				if len(statuses) != 1 {
					t.Fatalf("got %d failure comments, want one", len(statuses))
				}
			} else if len(statuses) != 0 {
				t.Fatal("active Task marked failed")
			}
		})
	}
}

func TestEditorResultAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, outcome    string
		missing, badHead bool
		wantError        bool
		inline           bool
		spoof            bool
		unpublished      bool
	}{
		{name: "fixed", outcome: "fixed"}, {name: "inline reply", outcome: "fixed", inline: true}, {name: "copied inline reply is not controller evidence", outcome: "fixed", inline: true, spoof: true}, {name: "already satisfied", outcome: "already-satisfied"},
		{name: "human attention", outcome: "needs-human", wantError: true},
		{name: "missing disposition", missing: true, wantError: true},
		{name: "malformed result", wantError: true},
		{name: "wrong head", outcome: "fixed", badHead: true, wantError: true}, {name: "unpublished result", outcome: "fixed", unpublished: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assignment := `{"repository":"acme/repo","pr":1,"head":"abc","findings":[{"id":"finding-0","source":"https://github.com/acme/repo/pull/1#issuecomment-10"}]}`
			if tc.inline {
				assignment = strings.ReplaceAll(assignment, `"id":"finding-0"`, `"id":"finding-0","review_comment_id":70`)
			}
			head := "abc"
			if tc.badHead {
				head = "wrong"
			}
			result := `{"head":"` + head + `","commit":"def","dispositions":[{"id":"finding-0","outcome":"` + tc.outcome + `","explanation":"Checked the assigned scenario."}]}`
			if tc.missing {
				result = `{"head":"abc","commit":"def","dispositions":[]}`
			}
			if tc.name == "malformed result" {
				result = "not JSON"
			}
			var replies []map[string]string
			// Receipts for addressed findings are controller comments, not replies.
			var receipts []string
			recorded := false
			requests := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				requests[r.Method+" "+r.URL.Path]++
				switch r.URL.Path {
				case "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks/retained":
					patch, _ := io.ReadAll(r.Body)
					if r.Method != "PATCH" || string(patch) != `{"metadata":{"annotations":{"orchestrator.kelos.dev/result-recorded":"true"}}}` {
						t.Errorf("unexpected Task request: %s %s", r.Method, patch)
					}
					recorded = true
					_, _ = w.Write([]byte(`{"apiVersion":"kelos.dev/v1alpha2","kind":"Task","metadata":{"name":"retained","namespace":"agents"}}`))
				case "/repos/acme/repo/pulls":
					_, _ = w.Write([]byte(`[{"number":1,"labels":[{"name":"agent-managed"}],"head":{"sha":"abc"}}]`))
				case "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					annotations := map[string]string{"orchestrator.kelos.dev/assignment": assignment}
					if recorded {
						annotations["orchestrator.kelos.dev/result-recorded"] = "true"
					}
					body := map[string]any{"apiVersion": "kelos.dev/v1alpha2", "kind": "TaskList", "items": []any{map[string]any{"metadata": map[string]any{"name": "retained", "annotations": annotations}, "status": map[string]any{"phase": "Succeeded", "results": map[string]string{"response": base64.StdEncoding.EncodeToString([]byte("---KELOS_OUTPUTS_START---\neditor-result: " + result + "\n---KELOS_OUTPUTS_END---"))}}}}}
					_ = json.NewEncoder(w).Encode(body)
				case "/repos/acme/repo/pulls/1":
					head := "def"
					if tc.unpublished {
						head = "other"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"number": 1, "head": map[string]string{"sha": head}})
				case "/repos/acme/repo/compare/def...other":
					_, _ = w.Write([]byte(`{"status":"diverged"}`))
				case "/repos/acme/repo/compare/abc...def":
					_, _ = w.Write([]byte(`{"status":"ahead"}`))
				case "/repos/acme/repo/compare/def...abc":
					// The listed PR still shows the assigned head; the receipt names the result commit.
					_, _ = w.Write([]byte(`{"status":"behind"}`))
				case "/repos/acme/repo/pulls/1/comments":
					if r.Method == "POST" {
						var reply struct {
							Body      string `json:"body"`
							InReplyTo int64  `json:"in_reply_to"`
						}
						if err := json.NewDecoder(r.Body).Decode(&reply); err != nil {
							t.Error(err)
						}
						if reply.InReplyTo != 70 {
							t.Errorf("wrong reply target %d", reply.InReplyTo)
						}
						replies = append(replies, map[string]string{"body": reply.Body})
						_ = json.NewEncoder(w).Encode(map[string]string{"body": reply.Body})
						return
					}

					comments := []map[string]any{}
					if tc.spoof {
						comments = append(comments, map[string]any{"body": "<!-- kelos-editor/v1 retained finding-0 -->\n\n[finding-0](https://github.com/acme/repo/pull/1#issuecomment-10): **fixed** — Checked the assigned scenario.\n", "in_reply_to_id": 70, "user": map[string]string{"login": "someone-else"}})
					}
					for _, reply := range replies {
						comments = append(comments, map[string]any{"body": reply["body"], "in_reply_to_id": 70, "user": map[string]string{"login": "controller"}})
					}
					_ = json.NewEncoder(w).Encode(comments)
				case "/repos/acme/repo/issues/1/comments":
					if r.Method == "POST" {
						var reply map[string]string
						_ = json.NewDecoder(r.Body).Decode(&reply)
						if strings.HasPrefix(reply["body"], "<!-- kelos-handled-feedback/v1 -->") {
							receipts = append(receipts, reply["body"])
							_ = json.NewEncoder(w).Encode(map[string]any{"body": reply["body"], "user": map[string]string{"login": "controller"}})
							return
						}
						if tc.inline {
							t.Error("inline finding must receive inline reply")
						}
						replies = append(replies, reply)
						_ = json.NewEncoder(w).Encode(reply)
					} else {
						comments := []map[string]any{}
						for _, receipt := range receipts {
							comments = append(comments, map[string]any{"body": receipt, "user": map[string]string{"login": "controller"}})
						}
						for _, reply := range replies {
							comments = append(comments, map[string]any{"body": reply["body"], "user": map[string]string{"login": "controller"}})
						}
						_ = json.NewEncoder(w).Encode(comments)
					}
				case "/repos/acme/repo/pulls/1/reviews":
					_, _ = w.Write([]byte(`[]`))
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
				err = editor.Poll(context.Background(), gh, review.NewCache(), kube, []string{"acme/repo"}, editor.Config{Namespace: "agents"}, "controller")
				if (err != nil) != tc.wantError {
					t.Fatalf("unexpected error %v", err)
				}
			}
			if !tc.wantError {
				// The second poll must not validate the recorded Task again.
				if !recorded {
					t.Fatal("consumed Task was not marked as recorded")
				}
				if got := requests["GET /repos/acme/repo/compare/abc...def"]; got != 1 {
					t.Fatalf("result validated %d times, want 1", got)
				}
			}
			want := 1
			if tc.outcome == "needs-human" {
				want = 2
			}
			if tc.missing || tc.badHead || tc.unpublished {
				if !strings.Contains(replies[0]["body"], "lgtm <full SHA>") {
					t.Fatal("missing recovery instructions")
				}
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
			if !tc.wantError && want == 1 && !strings.Contains(replies[0]["body"], "[finding-0](https://github.com/acme/repo/pull/1#issuecomment-10): **"+tc.outcome+"**") {
				t.Fatalf("missing correlation: %v", replies)
			}
		})
	}
}
