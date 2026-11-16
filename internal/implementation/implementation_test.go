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
					annotations := map[string]any{"kelos.dev/source-kind": "issue", "kelos.dev/source-number": "3"}
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
