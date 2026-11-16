package merge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v80/github"

	merge "github.com/hearthctl/kelos-orchestrator/internal/merge"
)

func TestGuardedMerge(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wantMerge bool
	}{
		{"automated approval without current review", false},
		{"approved current commit with enforced green CI", true},
		{"private plan cannot expose protection", false},
		{"edited approval is not reused", false},
		{"approval deleted before final check", false},
		{"new human feedback before final check", false}, {"retained deleted feedback invalidates approval", false},
		{"force push after approval invalidates it", false},
		{"human role revoked", false},
		{"ambiguous successful merge is recovered", true},
		{"stale approval stays revoked when head returns", false},
		{"latest pending check blocks old success", false},
		{"neutral is not green", false},
		{"unsupported ruleset blocks merge", false},
		{"check on a different head is rejected", false},
		{"merge response lost before mutation can retry next poll", false},
		{"failed test merge checks override green head", false},
		{"green test merge checks allow merge", true},
		{"hold prevents merge", false},
		{"draft prevents merge", false},
		{"required CI pending", false},
		{"required CI failed", false},
		{"required CI missing", false},
		{"required CI wrong app", false},
		{"unresolved review thread", false},
		{"admin bypass available", false},
		{"bare label is not approval", false},
		{"new head before merge", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			var ledger []*github.IssueComment
			merged := false
			gets := 0
			commentReads := 0
			mergeCalls := 0
			var mergeBody string
			phase := 0
			pr := func() string {
				head := sha
				if tc.name == "stale approval stays revoked when head returns" && phase == 1 {
					head = strings.Repeat("b", 40)
				}
				if tc.name == "new head before merge" && gets > 1 {
					head = strings.Repeat("b", 40)
				}
				labels := `{"name":"agent-managed"},{"name":"lgtm"}`
				if tc.name == "hold prevents merge" || (tc.name == "stale approval stays revoked when head returns" && phase == 0) {
					labels += `,{"name":"hold"}`
				}
				mergeSHA := ""
				if strings.Contains(tc.name, "test merge checks") {
					mergeSHA = strings.Repeat("c", 40)
				}
				return fmt.Sprintf(`{"merge_commit_sha":%q,"number":1,"state":"open","draft":%t,"merged":%t,"mergeable":true,"mergeable_state":"clean","head":{"sha":%q},"base":{"ref":"main","sha":"base"},"labels":[%s]}`, mergeSHA, tc.name == "draft prevents merge", merged, head, labels)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/merge"):
					mergeCalls++
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					b, _ := json.Marshal(payload)
					mergeBody = string(b)
					if tc.name == "merge response lost before mutation can retry next poll" {
						http.Error(w, `{"message":"timeout"}`, 500)
						return
					}
					merged = true
					if tc.name == "ambiguous successful merge is recovered" {
						http.Error(w, `{"message":"upstream disconnected"}`, 500)
						return
					}
					_, _ = w.Write([]byte(`{"merged":true,"sha":"merge-commit"}`))
					return
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/comments"):
					var comment github.IssueComment
					if err := json.NewDecoder(r.Body).Decode(&comment); err != nil {
						t.Error(err)
					}
					comment.ID = github.Ptr(int64(100 + len(ledger)))
					comment.User = &github.User{ID: github.Ptr(int64(90)), Login: github.Ptr("controller")}
					ledger = append(ledger, &comment)
					_ = json.NewEncoder(w).Encode(comment)
					return
				case r.Method == "PATCH" && strings.Contains(r.URL.Path, "/issues/comments/"):
					var comment github.IssueComment
					_ = json.NewDecoder(r.Body).Decode(&comment)
					for _, v := range ledger {
						if r.URL.Path == fmt.Sprintf("/repos/owner/repo/issues/comments/%d", v.GetID()) {
							v.Body = comment.Body
							_ = json.NewEncoder(w).Encode(v)
							return
						}
					}
					http.Error(w, "missing", 404)
					return
				case r.Method == "DELETE":
					w.WriteHeader(http.StatusNoContent)
					return
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/labels"):
					_, _ = w.Write([]byte(`[]`))
					return
				case r.URL.Path == "/graphql":
					resolved := tc.name != "unresolved review thread"
					_, _ = fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":%t}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}}`, resolved)
					return
				}
				var body string
				switch r.URL.Path {
				case "/repos/owner/repo/issues/comments/20":
					_, _ = w.Write([]byte(`{"user":{"login":"controller"}}`))
				case "/repos/owner/repo/actions/workflows/ocr.yml":
					_, _ = w.Write([]byte(`{"id":8}`))
				case "/repos/owner/repo/actions/workflows/8/runs":
					_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
				case "/repos/owner/repo/pulls":
					body = "[" + pr() + "]"
				case "/repos/owner/repo/pulls/1":
					gets++
					body = pr()
				case "/users/controller":
					body = `{"id":90,"login":"controller"}`
				case "/repos/owner/repo/issues/1/comments":
					commentReads++
					comments := append([]*github.IssueComment{}, ledger...)
					if tc.name != "automated approval without current review" && tc.name != "bare label is not approval" && (tc.name != "approval deleted before final check" || commentReads <= 1) {
						var human github.IssueComment
						_ = json.Unmarshal([]byte(`{"id":10,"body":"lgtm `+sha+`","user":{"id":1,"login":"geoberle"},"created_at":"2026-11-15T10:00:00Z","updated_at":"2026-11-15T10:00:00Z"}`), &human)
						if tc.name == "edited approval is not reused" {
							human.UpdatedAt = github.Ptr(github.Timestamp{Time: human.GetCreatedAt().Add(time.Second)})
						}
						comments = append(comments, &human)
					}
					if tc.name == "new human feedback before final check" && commentReads > 1 {
						var feedback github.IssueComment
						_ = json.Unmarshal([]byte(`{"id":11,"body":"Please fix this","user":{"login":"geoberle"},"created_at":"2026-11-15T10:01:00Z","updated_at":"2026-11-15T10:01:00Z"}`), &feedback)
						comments = append(comments, &feedback)
					}
					if tc.name == "retained deleted feedback invalidates approval" {
						var observation github.IssueComment
						_ = json.Unmarshal([]byte(`{"id":20,"body":"<!-- kelos-feedback/v1 -->\n[]","user":{"id":90,"login":"controller"},"created_at":"2026-11-15T10:01:00Z"}`), &observation)
						comments = append(comments, &observation)
					}
					if tc.name == "automated approval without current review" {
						history := fmt.Sprintf(`{"attempts":[{"task":"assessment-1","uid":"task-1","head":%q,"request":0,"started":"2026-11-15T10:00:00Z","state":"approve","reason":"Bounded and tested."}]}`, sha)
						comments = append(comments, &github.IssueComment{ID: github.Ptr(int64(20)), Body: github.Ptr("<!-- kelos-assessment/v1 -->\n" + history), User: &github.User{ID: github.Ptr(int64(90)), Login: github.Ptr("controller")}})
					}
					b, _ := json.Marshal(comments)
					body = string(b)
				case "/repos/owner/repo/pulls/1/comments", "/repos/owner/repo/pulls/1/reviews":
					body = `[]`
				case "/repos/owner/repo/issues/1/timeline":
					body = `[]`
					if tc.name == "force push after approval invalidates it" {
						body = `[{"event":"head_ref_force_pushed","created_at":"2026-11-15T10:01:00Z"}]`
					}
				case "/repos/owner/repo/collaborators/geoberle/permission":
					body = `{"permission":"admin","role_name":"admin"}`
					if tc.name == "human role revoked" {
						body = `{"permission":"read","role_name":"read"}`
					}
				case "/repos/owner/repo/branches/main/protection":
					if tc.name == "private plan cannot expose protection" {
						http.Error(w, `{"message":"Upgrade to GitHub Pro"}`, http.StatusForbidden)
						return
					}
					body = fmt.Sprintf(`{"enforce_admins":{"enabled":%t},"required_status_checks":{"strict":true,"checks":[{"context":"test","app_id":7}]},"required_conversation_resolution":{"enabled":true},"required_pull_request_reviews":{"bypass_pull_request_allowances":{"users":[],"teams":[],"apps":[]}}}`, tc.name != "admin bypass available")
				case "/repos/owner/repo/rules/branches/main":
					body = `[]`
					if tc.name == "unsupported ruleset blocks merge" {
						body = `[{"type":"future_policy"}]`
					}
				case "/repos/owner/repo/commits/" + sha + "/check-runs":
					status, conclusion, app := "completed", "success", 7
					if tc.name == "required CI pending" {
						status = "in_progress"
						conclusion = ""
					}
					if tc.name == "required CI failed" {
						conclusion = "failure"
					}
					if tc.name == "required CI wrong app" {
						app = 8
					}
					body = fmt.Sprintf(`{"check_runs":[{"id":20,"head_sha":%q,"name":"test","status":%q,"conclusion":%q,"app":{"id":%d}}]}`, sha, status, conclusion, app)
					if tc.name == "neutral is not green" {
						body = strings.ReplaceAll(body, `"success"`, `"neutral"`)
					}
					if tc.name == "check on a different head is rejected" {
						body = strings.ReplaceAll(body, sha, strings.Repeat("b", 40))
					}
					if tc.name == "latest pending check blocks old success" {
						body = strings.Replace(body, `"check_runs":[`, `"check_runs":[{"id":21,"head_sha":"`+sha+`","name":"test","status":"queued","app":{"id":7}},`, 1)
					}
					if tc.name == "required CI missing" {
						body = `{"check_runs":[]}`
					}
				case "/repos/owner/repo/commits/" + strings.Repeat("c", 40) + "/check-runs":
					conclusion := "success"
					if tc.name == "failed test merge checks override green head" {
						conclusion = "failure"
					}
					body = fmt.Sprintf(`{"check_runs":[{"id":30,"head_sha":%q,"name":"test","status":"completed","conclusion":%q,"app":{"id":7}}]}`, strings.Repeat("c", 40), conclusion)
				case "/repos/owner/repo/commits/" + strings.Repeat("c", 40) + "/statuses":
					body = `[]`
				case "/repos/owner/repo/commits/" + sha + "/statuses":
					body = `[]`
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			c := github.NewClient(server.Client())
			c.BaseURL, _ = url.Parse(server.URL + "/")
			// One reconciliation must be enough to merge an eligible PR; restart must not duplicate it.
			_ = merge.Poll(context.Background(), c, []string{"owner/repo"}, "controller")
			if tc.name == "stale approval stays revoked when head returns" {
				for phase = 1; phase <= 2; phase++ {
					_ = merge.Poll(context.Background(), c, []string{"owner/repo"}, "controller")
				}
			}
			if tc.name == "stale approval stays revoked when head returns" {
				if len(ledger) != 1 {
					t.Fatalf("approval records %d", len(ledger))
				}
				expected, err := os.ReadFile("testdata/revoked.golden")
				if err != nil {
					t.Fatal(err)
				}
				if ledger[0].GetBody() != strings.TrimSuffix(string(expected), "\n") {
					t.Fatalf("revocation evidence: %s", ledger[0].GetBody())
				}
			}
			if merged != tc.wantMerge {
				t.Fatalf("merged=%v want %v", merged, tc.wantMerge)
			}
			if tc.wantMerge {
				_ = merge.Poll(context.Background(), c, []string{"owner/repo"}, "controller")
				if mergeCalls != 1 {
					t.Fatalf("merge calls %d", mergeCalls)
				}
				want, err := os.ReadFile("testdata/merge.golden")
				if err != nil {
					t.Fatal(err)
				}
				if mergeBody != strings.TrimSpace(string(want)) {
					t.Fatalf("merge payload %s", mergeBody)
				}
			}
		})
	}
}
