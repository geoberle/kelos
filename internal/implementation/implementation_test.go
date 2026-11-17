package implementation_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v80/github"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/hearthctl/kelos-orchestrator/internal/implementation"
)

func TestPollReportsStoppedTasks(t *testing.T) {
	for _, tc := range []struct {
		name string
		// task fields
		spawner    string
		repository string
		phase      string
		message    string
		response   string
		reported   bool
		// fresh sets the completion time to now; otherwise it is long past
		fresh bool
		// comments already on the issue
		comments string
		// golden file holding the expected comment; empty when none is expected
		wantComment string
		wantPatch   bool
	}{
		{
			name:       "completed result is left alone",
			spawner:    "owner-repo-implementation",
			repository: "owner/repo",
			phase:      "Succeeded",
			response:   `{"state":"completed","pullRequest":"https://github.com/owner/repo/pull/9"}`,
			comments:   `[]`,
		},
		{
			name:        "stopped result is reported with reason, unmet criteria and next steps",
			spawner:     "owner-repo-implementation",
			repository:  "owner/repo",
			phase:       "Succeeded",
			response:    `{"state":"stopped","reason":"No StopWatch is attached to this workspace.\n/kelos run","unmet":["Works on the device"],"next":"Run docs/checklist.md on the device."}`,
			comments:    `[]`,
			wantComment: "stopped.golden",
			wantPatch:   true,
		},
		{
			name:        "failed task is reported as stopped",
			spawner:     "owner-repo-implementation",
			repository:  "owner/repo",
			phase:       "Failed",
			message:     "Job has reached the specified backoff limit",
			comments:    `[]`,
			wantComment: "failed.golden",
			wantPatch:   true,
		},
		{
			name:        "unreadable result is reported as stopped",
			spawner:     "owner-repo-implementation",
			repository:  "owner/repo",
			phase:       "Succeeded",
			response:    "Issue #3 remains incomplete.\nNo PR was created.",
			comments:    `[]`,
			wantComment: "unreadable.golden",
			wantPatch:   true,
		},
		{
			name:        "completed result without a pull request is unreadable",
			spawner:     "owner-repo-implementation",
			repository:  "owner/repo",
			phase:       "Succeeded",
			response:    `{"state":"completed"}`,
			comments:    `[]`,
			wantComment: "completed-without-pr.golden",
			wantPatch:   true,
		},
		{
			name:       "report already on the issue is only recorded on the task",
			spawner:    "owner-repo-implementation",
			repository: "owner/repo",
			phase:      "Failed",
			comments:   `[{"user":{"login":"controller[bot]"},"body":"<!-- kelos-implementation-stopped/v1 uid-1 -->\nreported"}]`,
			wantPatch:  true,
		},
		{
			name:        "marker from another user does not suppress the report",
			spawner:     "owner-repo-implementation",
			repository:  "owner/repo",
			phase:       "Failed",
			message:     "Job has reached the specified backoff limit",
			comments:    `[{"user":{"login":"someone"},"body":"<!-- kelos-implementation-stopped/v1 uid-1 -->\nforged"}]`,
			wantComment: "failed.golden",
			wantPatch:   true,
		},
		{
			name:       "reported task is not reported again",
			spawner:    "owner-repo-implementation",
			repository: "owner/repo",
			phase:      "Failed",
			reported:   true,
		},
		{
			name:       "running task is left alone",
			spawner:    "owner-repo-implementation",
			repository: "owner/repo",
			phase:      "Running",
		},
		{
			name:       "freshly finished task waits for Kelos to capture its final message",
			spawner:    "owner-repo-implementation",
			repository: "owner/repo",
			phase:      "Succeeded",
			fresh:      true,
		},
		{
			name:        "retried task is reported although its predecessor was",
			spawner:     "owner-repo-implementation",
			repository:  "owner/repo",
			phase:       "Failed",
			message:     "Job has reached the specified backoff limit",
			comments:    `[{"user":{"login":"controller[bot]"},"body":"<!-- kelos-implementation-stopped/v1 uid-0 -->\nreported"}]`,
			wantComment: "failed.golden",
			wantPatch:   true,
		},
		{
			name:        "comment opener in agent text cannot hide the report",
			spawner:     "owner-repo-implementation",
			repository:  "owner/repo",
			phase:       "Succeeded",
			response:    `{"state":"stopped","reason":"Blocked <!-- hidden"}`,
			comments:    `[]`,
			wantComment: "comment-opener.golden",
			wantPatch:   true,
		},
		{
			name:       "task of a repository outside the allowlist is left alone",
			spawner:    "other-implementation",
			repository: "other/repo",
			phase:      "Failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reported := tc.reported
			var posted []string
			patches := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "GET" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					// The in-progress label has its own test; here the issue is already labelled.
					annotations := map[string]any{"kelos.dev/source-kind": "issue", "kelos.dev/source-number": "3", "orchestrator.kelos.dev/in-progress-labelled": "true"}
					if reported {
						annotations["orchestrator.kelos.dev/stop-reported"] = "true"
					}
					// The API server filters by label; editor and assessment
					// Tasks carry no spawner label and are never listed.
					if r.URL.Query().Get("labelSelector") != "kelos.dev/taskspawner" {
						t.Errorf("unexpected label selector: %q", r.URL.Query().Get("labelSelector"))
					}
					labels := map[string]any{"kelos.dev/taskspawner": tc.spawner}
					completion := "2026-01-01T00:00:00Z"
					if tc.fresh {
						completion = time.Now().UTC().Format(time.RFC3339)
					}
					status := map[string]any{"phase": tc.phase, "message": tc.message, "completionTime": completion}
					if len(tc.response) > 0 {
						status["results"] = map[string]any{"response": base64.StdEncoding.EncodeToString([]byte(tc.response))}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"apiVersion": "kelos.dev/v1alpha2", "kind": "TaskList",
						"items": []any{map[string]any{
							"apiVersion": "kelos.dev/v1alpha2", "kind": "Task",
							"metadata": map[string]any{"name": "owner-repo-implementation-3", "namespace": "agents", "uid": "uid-1", "labels": labels, "annotations": annotations},
							"spec":     map[string]any{"branch": "agent/issue-3", "upstreamRepo": tc.repository},
							"status":   status,
						}},
					})
				case r.Method == "PATCH" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks/owner-repo-implementation-3":
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{"metadata":{"annotations":{"orchestrator.kelos.dev/stop-reported":"true"}}}` {
						t.Errorf("unexpected patch: %s", body)
					}
					patches++
					reported = true
					_, _ = w.Write([]byte(`{"apiVersion":"kelos.dev/v1alpha2","kind":"Task","metadata":{"name":"owner-repo-implementation-3","namespace":"agents"}}`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/issues":
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/issues/3/comments":
					_, _ = w.Write([]byte(tc.comments))
				case r.Method == "POST" && r.URL.Path == "/repos/owner/repo/issues/3/comments":
					var comment struct {
						Body string `json:"body"`
					}
					if err := json.NewDecoder(r.Body).Decode(&comment); err != nil {
						t.Error(err)
					}
					posted = append(posted, comment.Body)
					_, _ = w.Write([]byte(`{}`))
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
			// A second poll must not report the same Task again.
			for range 2 {
				if err := implementation.Poll(context.Background(), client, kube, []string{"owner/repo"}, implementation.Config{Namespace: "agents"}, "controller[bot]"); err != nil {
					t.Fatal(err)
				}
			}
			wantPatches := 0
			if tc.wantPatch {
				wantPatches = 1
			}
			if patches != wantPatches {
				t.Fatalf("patches=%d want %d", patches, wantPatches)
			}
			if len(tc.wantComment) == 0 {
				if len(posted) != 0 {
					t.Fatalf("unexpected comments: %q", posted)
				}
				return
			}
			if len(posted) != 1 {
				t.Fatalf("comments=%d want 1: %q", len(posted), posted)
			}
			// Kelos treats a line that equals the command as a trigger; the
			// controller is an allowed user, so its report must never contain one.
			for _, line := range strings.Split(posted[0], "\n") {
				if strings.TrimSpace(line) == "/kelos run" {
					t.Fatalf("report would retrigger the Task:\n%s", posted[0])
				}
			}
			want, err := os.ReadFile("testdata/" + tc.wantComment)
			if err != nil {
				t.Fatal(err)
			}
			if posted[0] != string(want) {
				t.Fatalf("comment:\n%s\nwant:\n%s", posted[0], want)
			}
		})
	}
}

func TestPollLinksTheLogOfRunningTasks(t *testing.T) {
	for _, tc := range []struct {
		name string
		// consoleURL is the configured Console origin; empty disables the link
		consoleURL string
		phase      string
		response   string
		linked     bool
		// comments already on the issue
		comments string
		// golden file holding the expected comment; empty when none is expected
		wantComment string
		wantPatch   bool
	}{
		{
			name:        "running task gets a link to its log",
			consoleURL:  "https://kelos.example",
			phase:       "Running",
			comments:    `[]`,
			wantComment: "log-link.golden",
			wantPatch:   true,
		},
		{
			name:        "task that has not started yet gets a link to its log",
			consoleURL:  "https://kelos.example",
			phase:       "",
			comments:    `[]`,
			wantComment: "log-link.golden",
			wantPatch:   true,
		},
		{
			name:       "no link without a configured console",
			consoleURL: "",
			phase:      "Running",
		},
		{
			name:       "finished task gets no link",
			consoleURL: "https://kelos.example",
			phase:      "Succeeded",
			response:   `{"state":"completed","pullRequest":"https://github.com/owner/repo/pull/9"}`,
		},
		{
			name:       "linked task is not linked again",
			consoleURL: "https://kelos.example",
			phase:      "Running",
			linked:     true,
		},
		{
			name:       "link already on the issue is only recorded on the task",
			consoleURL: "https://kelos.example",
			phase:      "Running",
			comments:   `[{"user":{"login":"controller[bot]"},"body":"<!-- kelos-implementation-log/v1 uid-1 -->\nlinked"}]`,
			wantPatch:  true,
		},
		{
			name:        "marker from another user does not suppress the link",
			consoleURL:  "https://kelos.example",
			phase:       "Running",
			comments:    `[{"user":{"login":"someone"},"body":"<!-- kelos-implementation-log/v1 uid-1 -->\nforged"}]`,
			wantComment: "log-link.golden",
			wantPatch:   true,
		},
		{
			name:        "retried task is linked although its predecessor was",
			consoleURL:  "https://kelos.example",
			phase:       "Running",
			comments:    `[{"user":{"login":"controller[bot]"},"body":"<!-- kelos-implementation-log/v1 uid-0 -->\nlinked"}]`,
			wantComment: "log-link.golden",
			wantPatch:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			linked := tc.linked
			var posted []string
			patches := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "GET" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					// The in-progress label has its own test; here the issue is already labelled.
					annotations := map[string]any{"kelos.dev/source-kind": "issue", "kelos.dev/source-number": "3", "orchestrator.kelos.dev/in-progress-labelled": "true"}
					if linked {
						annotations["orchestrator.kelos.dev/log-linked"] = "true"
					}
					status := map[string]any{"phase": tc.phase, "completionTime": "2026-01-01T00:00:00Z"}
					if len(tc.response) > 0 {
						status["results"] = map[string]any{"response": base64.StdEncoding.EncodeToString([]byte(tc.response))}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"apiVersion": "kelos.dev/v1alpha2", "kind": "TaskList",
						"items": []any{map[string]any{
							"apiVersion": "kelos.dev/v1alpha2", "kind": "Task",
							"metadata": map[string]any{"name": "owner-repo-implementation-3", "namespace": "agents", "uid": "uid-1", "labels": map[string]any{"kelos.dev/taskspawner": "owner-repo-implementation"}, "annotations": annotations},
							"spec":     map[string]any{"branch": "agent/issue-3", "upstreamRepo": "owner/repo"},
							"status":   status,
						}},
					})
				case r.Method == "PATCH" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks/owner-repo-implementation-3":
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{"metadata":{"annotations":{"orchestrator.kelos.dev/log-linked":"true"}}}` {
						t.Errorf("unexpected patch: %s", body)
					}
					patches++
					linked = true
					_, _ = w.Write([]byte(`{"apiVersion":"kelos.dev/v1alpha2","kind":"Task","metadata":{"name":"owner-repo-implementation-3","namespace":"agents"}}`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/issues":
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/issues/3/comments":
					_, _ = w.Write([]byte(tc.comments))
				case r.Method == "POST" && r.URL.Path == "/repos/owner/repo/issues/3/comments":
					var comment struct {
						Body string `json:"body"`
					}
					if err := json.NewDecoder(r.Body).Decode(&comment); err != nil {
						t.Error(err)
					}
					posted = append(posted, comment.Body)
					_, _ = w.Write([]byte(`{}`))
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
			// A second poll must not link the same Task again.
			for range 2 {
				if err := implementation.Poll(context.Background(), client, kube, []string{"owner/repo"}, implementation.Config{Namespace: "agents", ConsoleURL: tc.consoleURL}, "controller[bot]"); err != nil {
					t.Fatal(err)
				}
			}
			wantPatches := 0
			if tc.wantPatch {
				wantPatches = 1
			}
			if patches != wantPatches {
				t.Fatalf("patches=%d want %d", patches, wantPatches)
			}
			if len(tc.wantComment) == 0 {
				if len(posted) != 0 {
					t.Fatalf("unexpected comments: %q", posted)
				}
				return
			}
			if len(posted) != 1 {
				t.Fatalf("comments=%d want 1: %q", len(posted), posted)
			}
			want, err := os.ReadFile("testdata/" + tc.wantComment)
			if err != nil {
				t.Fatal(err)
			}
			if posted[0] != string(want) {
				t.Fatalf("comment:\n%s\nwant:\n%s", posted[0], want)
			}
		})
	}
}

func TestPollLabelsIssuesInProgress(t *testing.T) {
	for _, tc := range []struct {
		name string
		// task fields; no Task exists when hasTask is false
		hasTask  bool
		phase    string
		response string
		reported bool
		labelled bool
		// fresh sets the completion time to now; otherwise it is long past
		fresh bool
		// issue is the issue as GitHub returns it
		issue string
		// labelledIssues is GitHub's list of issues carrying the label
		labelledIssues string
		wantAdd        bool
		wantRemove     bool
		wantPatch      bool
	}{
		{
			name:           "running task labels its open issue",
			hasTask:        true,
			phase:          "Running",
			issue:          `{"number":3,"state":"open"}`,
			labelledIssues: `[]`,
			wantAdd:        true,
			wantPatch:      true,
		},
		{
			name:           "task that opened a pull request labels its open issue",
			hasTask:        true,
			phase:          "Succeeded",
			response:       `{"state":"completed","pullRequest":"https://github.com/owner/repo/pull/9"}`,
			issue:          `{"number":3,"state":"open"}`,
			labelledIssues: `[]`,
			wantAdd:        true,
			wantPatch:      true,
		},
		{
			name:           "task of a closed issue is only recorded",
			hasTask:        true,
			phase:          "Succeeded",
			response:       `{"state":"completed","pullRequest":"https://github.com/owner/repo/pull/9"}`,
			issue:          `{"number":3,"state":"closed"}`,
			labelledIssues: `[]`,
			wantPatch:      true,
		},
		{
			name:           "label stays while the task runs",
			hasTask:        true,
			phase:          "Running",
			labelled:       true,
			labelledIssues: `[{"number":3,"state":"open"}]`,
		},
		{
			name:           "label stays while the pull request is open",
			hasTask:        true,
			phase:          "Succeeded",
			response:       `{"state":"completed","pullRequest":"https://github.com/owner/repo/pull/9"}`,
			labelled:       true,
			labelledIssues: `[{"number":3,"state":"open"}]`,
		},
		{
			name:           "label stays while Kelos captures the final message",
			hasTask:        true,
			phase:          "Succeeded",
			fresh:          true,
			labelled:       true,
			labelledIssues: `[{"number":3,"state":"open"}]`,
		},
		{
			name:           "label is removed once the issue is closed",
			hasTask:        true,
			phase:          "Succeeded",
			response:       `{"state":"completed","pullRequest":"https://github.com/owner/repo/pull/9"}`,
			labelled:       true,
			labelledIssues: `[{"number":3,"state":"closed"}]`,
			wantRemove:     true,
		},
		{
			name:           "label is removed when the task stopped without a pull request",
			hasTask:        true,
			phase:          "Failed",
			reported:       true,
			labelled:       true,
			labelledIssues: `[{"number":3,"state":"open"}]`,
			wantRemove:     true,
		},
		{
			name:           "stopped task does not label its issue",
			hasTask:        true,
			phase:          "Failed",
			reported:       true,
			labelledIssues: `[]`,
		},
		{
			name:           "label is removed when the task is gone",
			labelledIssues: `[{"number":3,"state":"open"}]`,
			wantRemove:     true,
		},
		{
			name:           "labelled pull request is left alone",
			labelledIssues: `[{"number":3,"state":"open","pull_request":{"url":"https://api.github.com/repos/owner/repo/pulls/3"}}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			labelled := tc.labelled
			labelledIssues := tc.labelledIssues
			adds, removes, patches := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "GET" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks":
					items := []any{}
					if tc.hasTask {
						annotations := map[string]any{"kelos.dev/source-kind": "issue", "kelos.dev/source-number": "3"}
						if tc.reported {
							annotations["orchestrator.kelos.dev/stop-reported"] = "true"
						}
						if labelled {
							annotations["orchestrator.kelos.dev/in-progress-labelled"] = "true"
						}
						completion := "2026-01-01T00:00:00Z"
						if tc.fresh {
							completion = time.Now().UTC().Format(time.RFC3339)
						}
						status := map[string]any{"phase": tc.phase, "completionTime": completion}
						if len(tc.response) > 0 {
							status["results"] = map[string]any{"response": base64.StdEncoding.EncodeToString([]byte(tc.response))}
						}
						items = append(items, map[string]any{
							"apiVersion": "kelos.dev/v1alpha2", "kind": "Task",
							"metadata": map[string]any{"name": "owner-repo-implementation-3", "namespace": "agents", "uid": "uid-1", "labels": map[string]any{"kelos.dev/taskspawner": "owner-repo-implementation"}, "annotations": annotations},
							"spec":     map[string]any{"branch": "agent/issue-3", "upstreamRepo": "owner/repo"},
							"status":   status,
						})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "kelos.dev/v1alpha2", "kind": "TaskList", "items": items})
				case r.Method == "PATCH" && r.URL.Path == "/apis/kelos.dev/v1alpha2/namespaces/agents/tasks/owner-repo-implementation-3":
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{"metadata":{"annotations":{"orchestrator.kelos.dev/in-progress-labelled":"true"}}}` {
						t.Errorf("unexpected patch: %s", body)
					}
					patches++
					labelled = true
					_, _ = w.Write([]byte(`{"apiVersion":"kelos.dev/v1alpha2","kind":"Task","metadata":{"name":"owner-repo-implementation-3","namespace":"agents"}}`))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/issues":
					if r.URL.Query().Get("labels") != "kelos-in-progress" || r.URL.Query().Get("state") != "all" {
						t.Errorf("unexpected issue query: %s", r.URL.RawQuery)
					}
					_, _ = w.Write([]byte(labelledIssues))
				case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/issues/3":
					_, _ = w.Write([]byte(tc.issue))
				case r.Method == "POST" && r.URL.Path == "/repos/owner/repo/issues/3/labels":
					body, _ := io.ReadAll(r.Body)
					if string(body) != `["kelos-in-progress"]`+"\n" {
						t.Errorf("unexpected labels: %s", body)
					}
					adds++
					labelledIssues = `[{"number":3,"state":"open"}]`
					_, _ = w.Write([]byte(`[]`))
				case r.Method == "DELETE" && r.URL.Path == "/repos/owner/repo/issues/3/labels/kelos-in-progress":
					removes++
					labelledIssues = `[]`
					_, _ = w.Write([]byte(`[]`))
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
			// A second poll must change nothing more.
			for range 2 {
				if err := implementation.Poll(context.Background(), client, kube, []string{"owner/repo"}, implementation.Config{Namespace: "agents"}, "controller[bot]"); err != nil {
					t.Fatal(err)
				}
			}
			want := func(b bool) int {
				if b {
					return 1
				}
				return 0
			}
			if adds != want(tc.wantAdd) || removes != want(tc.wantRemove) || patches != want(tc.wantPatch) {
				t.Fatalf("adds=%d removes=%d patches=%d want %d %d %d", adds, removes, patches, want(tc.wantAdd), want(tc.wantRemove), want(tc.wantPatch))
			}
		})
	}
}
