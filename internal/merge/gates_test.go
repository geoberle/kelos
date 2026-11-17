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
	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

func TestGuardedMerge(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wantMerge bool
	}{
		{"automated approval without current review", false},
		{"approved current commit with enforced green CI", true},
		{"unavailable rules block merge", false},
		{"edited approval is not reused", false},
		{"approval deleted before final check", false},
		{"new human feedback before final check", false}, {"retained deleted feedback invalidates approval", false},
		{"force push after approval invalidates it", false},
		{"push after approval is not approved", false},
		{"commit that returns to the branch after approval is not approved", false},
		{"branch without a push log entry for the head is not approved", false},
		{"human role revoked", false},
		{"ambiguous successful merge is recovered", true},
		{"stale approval stays revoked when head returns", false},
		{"latest pending check blocks old success", false},
		{"neutral check does not block merge", true},
		{"skipped check does not block merge", true},
		{"cancelled check blocks merge", false},
		{"timed out check blocks merge", false},
		{"failed commit status blocks merge", false},
		{"target without pull request rule blocks merge", false},
		{"linear history blocks merge", false},
		{"merge method restriction blocks merge", false},
		{"other rules do not block merge", true},
		{"required check that has not run blocks merge and is named once", false},
		{"required check reported as a commit status allows merge", true},
		{"check on a different head is rejected", false},
		{"merge response lost before mutation can retry next poll", false},
		{"failed test merge checks override green head", false},
		{"green test merge checks allow merge", true},
		{"hold prevents merge", false},
		{"draft prevents merge", false},
		{"pending check blocks merge", false},
		{"failed check blocks merge", false},
		{"no checks at all does not block merge", true},
		{"unresolved review thread", false},
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
				return fmt.Sprintf(`{"merge_commit_sha":%q,"number":1,"state":"open","draft":%t,"merged":%t,"mergeable":true,"mergeable_state":"clean","head":{"sha":%q,"ref":"agent-fix","repo":{"full_name":"owner/repo"}},"base":{"ref":"main","sha":"base"},"labels":[%s]}`, mergeSHA, tc.name == "draft prevents merge", merged, head, labels)
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
						_ = json.Unmarshal([]byte(`{"id":10,"body":"/lgtm","user":{"id":1,"login":"geoberle"},"created_at":"2026-11-15T10:00:00Z","updated_at":"2026-11-15T10:00:00Z"}`), &human)
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
				case "/repos/owner/repo/rules/branches/main":
					// Effective rules, including those of an organization ruleset.
					body = `[{"type":"pull_request","parameters":{"required_approving_review_count":0}}]`
					switch tc.name {
					case "unavailable rules block merge":
						http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
						return
					case "target without pull request rule blocks merge":
						body = `[{"type":"deletion"},{"type":"non_fast_forward"}]`
					case "linear history blocks merge":
						body = `[{"type":"pull_request","parameters":{}},{"type":"required_linear_history"}]`
					case "merge method restriction blocks merge":
						body = `[{"type":"pull_request","parameters":{"allowed_merge_methods":["squash","rebase"]}}]`
					case "required check that has not run blocks merge and is named once":
						// The commit only has the check "test".
						body = `[{"type":"pull_request","parameters":{}},{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"verify"},{"context":"test"}]}}]`
					case "required check reported as a commit status allows merge":
						body = `[{"type":"pull_request","parameters":{}},{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"verify-base"}]}}]`
					case "other rules do not block merge":
						body = `[{"type":"pull_request","parameters":{"allowed_merge_methods":["merge","squash"]}},{"type":"deletion"},{"type":"non_fast_forward"},{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"test"}]}},{"type":"future_policy"}]`
					}
				case "/repos/owner/repo/commits/" + sha + "/check-runs":
					status, conclusion, app := "completed", "success", 7
					if tc.name == "pending check blocks merge" {
						status = "in_progress"
						conclusion = ""
					}
					switch tc.name {
					case "failed check blocks merge":
						conclusion = "failure"
					case "cancelled check blocks merge":
						conclusion = "cancelled"
					case "timed out check blocks merge":
						conclusion = "timed_out"
					case "neutral check does not block merge":
						conclusion = "neutral"
					case "skipped check does not block merge":
						conclusion = "skipped"
					}
					body = fmt.Sprintf(`{"check_runs":[{"id":20,"head_sha":%q,"name":"test","status":%q,"conclusion":%q,"app":{"id":%d}}]}`, sha, status, conclusion, app)
					if tc.name == "check on a different head is rejected" {
						body = strings.ReplaceAll(body, sha, strings.Repeat("b", 40))
					}
					if tc.name == "latest pending check blocks old success" {
						body = strings.Replace(body, `"check_runs":[`, `"check_runs":[{"id":21,"head_sha":"`+sha+`","name":"test","status":"queued","app":{"id":7}},`, 1)
					}
					if tc.name == "no checks at all does not block merge" {
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
				case "/repos/owner/repo/activity":
					// GitHub's push log for the PR branch, newest first. The approval comment is from 10:00.
					if r.URL.Query().Get("ref") != "refs/heads/agent-fix" {
						t.Errorf("push log read for %q", r.URL.Query().Get("ref"))
					}
					body = `[{"activity_type":"push","ref":"refs/heads/agent-fix","after":"` + sha + `","timestamp":"2026-11-15T09:00:00Z"}]`
					switch tc.name {
					case "push after approval is not approved":
						body = `[{"activity_type":"push","ref":"refs/heads/agent-fix","after":"` + sha + `","timestamp":"2026-11-15T10:00:30Z"}]`
					case "commit that returns to the branch after approval is not approved":
						// The same commit was the head at 08:00, was replaced, and was pushed again after the comment.
						body = `[{"activity_type":"push","ref":"refs/heads/agent-fix","after":"` + sha + `","timestamp":"2026-11-15T10:00:30Z"},{"activity_type":"force_push","ref":"refs/heads/agent-fix","after":"` + strings.Repeat("d", 40) + `","timestamp":"2026-11-15T08:30:00Z"},{"activity_type":"push","ref":"refs/heads/agent-fix","after":"` + sha + `","timestamp":"2026-11-15T08:00:00Z"}]`
					case "branch without a push log entry for the head is not approved":
						body = `[{"activity_type":"push","ref":"refs/heads/agent-fix","after":"` + strings.Repeat("d", 40) + `","timestamp":"2026-11-15T09:00:00Z"}]`
					}
				case "/repos/owner/repo/git/ref/heads/main":
					body = `{"ref":"refs/heads/main","object":{"type":"commit","sha":"` + strings.Repeat("e", 40) + `"}}`
				case "/repos/owner/repo/commits/" + sha + "/statuses":
					// The controller's verify result for the head on the current base.
					verified := `{"context":"verify-base","state":"success","description":"verify passed BaseSHA:` + strings.Repeat("e", 40) + `","creator":{"id":90}}`
					body = `[` + verified + `]`
					if tc.name == "failed commit status blocks merge" {
						body = `[{"context":"legacy","state":"failure"},` + verified + `]`
					}
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
			_ = merge.Poll(context.Background(), c, review.NewCache(), []string{"owner/repo"}, "controller")
			if tc.name == "stale approval stays revoked when head returns" {
				for phase = 1; phase <= 2; phase++ {
					_ = merge.Poll(context.Background(), c, review.NewCache(), []string{"owner/repo"}, "controller")
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
			// A second poll must not name the block again.
			if tc.name == "required check that has not run blocks merge and is named once" {
				_ = merge.Poll(context.Background(), c, review.NewCache(), []string{"owner/repo"}, "controller")
			}
			var blocked []string
			for _, comment := range ledger {
				if strings.HasPrefix(comment.GetBody(), "<!-- kelos-merge-blocked/v1 ") {
					blocked = append(blocked, comment.GetBody())
				}
			}
			if tc.name == "required check that has not run blocks merge and is named once" {
				want, err := os.ReadFile("testdata/blocked.golden")
				if err != nil {
					t.Fatal(err)
				}
				if len(blocked) != 1 || blocked[0] != strings.TrimSuffix(string(want), "\n") {
					t.Fatalf("block notices: %q", blocked)
				}
			} else if len(blocked) != 0 {
				t.Fatalf("unexpected block notices: %q", blocked)
			}
			if tc.wantMerge {
				_ = merge.Poll(context.Background(), c, review.NewCache(), []string{"owner/repo"}, "controller")
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
