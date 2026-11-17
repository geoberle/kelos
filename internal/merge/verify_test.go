package merge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/go-github/v80/github"

	merge "github.com/hearthctl/kelos-orchestrator/internal/merge"
	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

// The fake repository has approved pull requests 1 and 2 targeting main.
// Their heads are forty times "1" and "2". The golden file of a case lists
// every write one poll sends, and the error the poll reports.
func TestMergeOnlyOnVerifiedCurrentBase(t *testing.T) {
	const (
		head1   = "1111111111111111111111111111111111111111"
		head2   = "2222222222222222222222222222222222222222"
		base    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		oldBase = "cccccccccccccccccccccccccccccccccccccccc"
		// The base after a merge during the poll.
		newBase = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	)
	type pull struct {
		number         int
		head           string
		mergeable      string
		mergeableState string
		labels         []string
		// Commit statuses of the head, newest first.
		statuses string
	}
	for _, tc := range []struct {
		name  string
		pulls []pull
		// Workflow runs by ID.
		runs   map[string]string
		golden string
	}{
		{
			name: "verify passed on the current base",
			pulls: []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "clean", labels: []string{"lgtm"}, statuses: `[
				{"context":"verify-base","state":"success","description":"verify passed BaseSHA:` + base + `","target_url":"https://github.com/owner/repo/actions/runs/7","creator":{"id":90}}]`}},
			golden: "verify-base-unchanged",
		},
		{
			name: "base moved since verify passed",
			pulls: []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "clean", labels: []string{"lgtm"}, statuses: `[
				{"context":"verify-base","state":"success","description":"verify passed BaseSHA:` + oldBase + `","target_url":"https://github.com/owner/repo/actions/runs/7","creator":{"id":90}}]`}},
			golden: "verify-base-moved",
		},
		{
			name:   "never verified",
			pulls:  []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "clean", labels: []string{"lgtm"}, statuses: `[]`}},
			golden: "verify-base-moved",
		},
		{
			name: "result written by another identity is not a result",
			pulls: []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "clean", labels: []string{"lgtm"}, statuses: `[
				{"context":"verify-base","state":"success","description":"verify passed BaseSHA:` + base + `","target_url":"https://github.com/owner/repo/actions/runs/7","creator":{"id":41}}]`}},
			golden: "verify-base-moved",
		},
		{
			name: "re-run pending",
			pulls: []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "unstable", labels: []string{"lgtm"}, statuses: `[
				{"context":"verify-base","state":"pending","description":"verify is running BaseSHA:` + base + `","target_url":"https://github.com/owner/repo/actions/runs/8","creator":{"id":90}},
				{"context":"verify-base","state":"success","description":"verify passed BaseSHA:` + oldBase + `","target_url":"https://github.com/owner/repo/actions/runs/7","creator":{"id":90}}]`}},
			runs:   map[string]string{"8": `{"id":8,"status":"in_progress"}`},
			golden: "verify-pending",
		},
		{
			name: "re-run finished green",
			pulls: []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "unstable", labels: []string{"lgtm"}, statuses: `[
				{"context":"verify-base","state":"pending","description":"verify is running BaseSHA:` + base + `","target_url":"https://github.com/owner/repo/actions/runs/8","creator":{"id":90}}]`}},
			runs:   map[string]string{"8": `{"id":8,"status":"completed","conclusion":"success"}`},
			golden: "verify-finished-green",
		},
		{
			name: "re-run finished red",
			pulls: []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "unstable", labels: []string{"lgtm"}, statuses: `[
				{"context":"verify-base","state":"pending","description":"verify is running BaseSHA:` + base + `","target_url":"https://github.com/owner/repo/actions/runs/8","creator":{"id":90}}]`}},
			runs:   map[string]string{"8": `{"id":8,"status":"completed","conclusion":"failure"}`},
			golden: "verify-finished-red",
		},
		{
			name: "re-run red",
			pulls: []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "unstable", labels: []string{"lgtm"}, statuses: `[
				{"context":"verify-base","state":"failure","description":"verify failed BaseSHA:` + base + `","target_url":"https://github.com/owner/repo/actions/runs/8","creator":{"id":90}}]`}},
			golden: "verify-red",
		},
		{
			name: "base moved since verify failed",
			pulls: []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "unstable", labels: []string{"lgtm"}, statuses: `[
				{"context":"verify-base","state":"failure","description":"verify failed BaseSHA:` + oldBase + `","target_url":"https://github.com/owner/repo/actions/runs/7","creator":{"id":90}}]`}},
			golden: "verify-base-moved",
		},
		{
			name:   "conflict",
			pulls:  []pull{{number: 1, head: head1, mergeable: "false", mergeableState: "dirty", labels: []string{"lgtm"}, statuses: `[]`}},
			golden: "verify-conflict",
		},
		{
			name:   "conflict already reported",
			pulls:  []pull{{number: 1, head: head1, mergeable: "false", mergeableState: "dirty", labels: []string{"lgtm", "needs-rebase"}, statuses: `[]`}},
			golden: "verify-conflict-reported",
		},
		{
			name:   "conflict gone",
			pulls:  []pull{{number: 1, head: head1, mergeable: "true", mergeableState: "unstable", labels: []string{"lgtm", "needs-rebase"}, statuses: `[]`}},
			golden: "verify-conflict-gone",
		},
		{
			name:   "mergeability not computed yet",
			pulls:  []pull{{number: 1, head: head1, mergeable: "null", mergeableState: "unknown", labels: []string{"lgtm", "needs-rebase"}, statuses: `[]`}},
			golden: "verify-mergeability-unknown",
		},
		{
			name: "two unverified pull requests are verified one at a time",
			pulls: []pull{
				{number: 2, head: head2, mergeable: "true", mergeableState: "clean", labels: []string{"lgtm"}, statuses: `[]`},
				{number: 1, head: head1, mergeable: "true", mergeableState: "clean", labels: []string{"lgtm"}, statuses: `[]`},
			},
			golden: "verify-base-moved",
		},
		{
			name: "nothing is triggered while another pull request is being verified",
			pulls: []pull{
				{number: 2, head: head2, mergeable: "true", mergeableState: "unstable", labels: []string{"lgtm"}, statuses: `[
					{"context":"verify-base","state":"pending","description":"verify is running BaseSHA:` + base + `","target_url":"https://github.com/owner/repo/actions/runs/8","creator":{"id":90}}]`},
				{number: 1, head: head1, mergeable: "true", mergeableState: "clean", labels: []string{"lgtm"}, statuses: `[]`},
			},
			runs:   map[string]string{"8": `{"id":8,"status":"queued"}`},
			golden: "verify-pending",
		},
		{
			name: "second pull request is verified on the base that contains the first",
			pulls: []pull{
				{number: 1, head: head1, mergeable: "true", mergeableState: "clean", labels: []string{"lgtm"}, statuses: `[
					{"context":"verify-base","state":"success","description":"verify passed BaseSHA:` + base + `","target_url":"https://github.com/owner/repo/actions/runs/7","creator":{"id":90}}]`},
				{number: 2, head: head2, mergeable: "true", mergeableState: "clean", labels: []string{"lgtm"}, statuses: `[
					{"context":"verify-base","state":"success","description":"verify passed BaseSHA:` + base + `","target_url":"https://github.com/owner/repo/actions/runs/6","creator":{"id":90}}]`},
			},
			golden: "verify-second-after-first",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mutations []string
			tip := base
			merged := map[int]bool{}
			find := func(path string) *pull {
				for i, p := range tc.pulls {
					for _, at := range []string{fmt.Sprintf("/pulls/%d", p.number), fmt.Sprintf("/issues/%d", p.number), "/commits/" + p.head} {
						if strings.HasSuffix(path, at) || strings.Contains(path, at+"/") {
							return &tc.pulls[i]
						}
					}
				}
				return nil
			}
			render := func(p pull) string {
				labels := `{"name":"agent-managed"}`
				for _, l := range p.labels {
					labels += fmt.Sprintf(`,{"name":%q}`, l)
				}
				return fmt.Sprintf(`{"number":%d,"state":"open","merged":%t,"mergeable":%s,"mergeable_state":%q,"head":{"sha":%q,"ref":"agent-%d","repo":{"full_name":"owner/repo"}},"base":{"ref":"main","sha":%q},"labels":[%s]}`,
					p.number, merged[p.number], p.mergeable, p.mergeableState, p.head, p.number, oldBase, labels)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				path := r.URL.Path
				p := find(path)
				if path == "/graphql" {
					_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}}`))
					return
				}
				if r.Method != "GET" {
					raw, _ := io.ReadAll(r.Body)
					var payload any
					line := r.Method + " " + path
					if json.Unmarshal(raw, &payload) == nil {
						// Marshalling a decoded payload sorts its keys.
						sorted, _ := json.Marshal(payload)
						line += " " + string(sorted)
					}
					mutations = append(mutations, line)
					switch {
					case r.Method == "PUT" && strings.HasSuffix(path, "/merge"):
						merged[p.number] = true
						tip = newBase
						_, _ = w.Write([]byte(`{"merged":true}`))
					case path == "/repos/owner/repo/actions/workflows/ci.yml/dispatches":
						_, _ = w.Write([]byte(`{"workflow_run_id":9,"run_url":"https://api.github.com/repos/owner/repo/actions/runs/9","html_url":"https://github.com/owner/repo/actions/runs/9"}`))
					case r.Method == "POST" && strings.HasPrefix(path, "/repos/owner/repo/statuses/"):
						_, _ = w.Write([]byte(`{}`))
					case strings.Contains(path, "/labels"):
						_, _ = w.Write([]byte(`[]`))
					default:
						t.Errorf("unexpected %s %s", r.Method, r.URL)
						http.Error(w, "unexpected", 500)
					}
					return
				}
				var body string
				switch {
				case path == "/repos/owner/repo/pulls":
					var list []string
					for _, each := range tc.pulls {
						list = append(list, render(each))
					}
					body = "[" + strings.Join(list, ",") + "]"
				case path == "/users/controller":
					body = `{"id":90,"login":"controller"}`
				case path == "/repos/owner/repo/collaborators/geoberle/permission":
					body = `{"permission":"admin","role_name":"admin"}`
				case path == "/repos/owner/repo/rules/branches/main":
					body = `[{"type":"pull_request","parameters":{}}]`
				case path == "/repos/owner/repo/git/ref/heads/main":
					body = fmt.Sprintf(`{"ref":"refs/heads/main","object":{"type":"commit","sha":%q}}`, tip)
				case strings.HasPrefix(path, "/repos/owner/repo/actions/runs/"):
					body = tc.runs[strings.TrimPrefix(path, "/repos/owner/repo/actions/runs/")]
					if len(body) == 0 {
						t.Errorf("unexpected run read %s", path)
					}
				case path == "/repos/owner/repo/activity":
					// The push log of the branch: the head was pushed an hour before the approval.
					number := strings.TrimPrefix(r.URL.Query().Get("ref"), "refs/heads/agent-")
					after := head1
					if number == "2" {
						after = head2
					}
					body = fmt.Sprintf(`[{"activity_type":"push","after":%q,"timestamp":"2026-11-15T09:00:00Z"}]`, after)
				case p == nil:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
					return
				case strings.HasSuffix(path, fmt.Sprintf("/pulls/%d", p.number)):
					body = render(*p)
				case strings.HasSuffix(path, fmt.Sprintf("/issues/%d/comments", p.number)):
					// The human approval and the controller's record of it.
					record, _ := json.Marshal(fmt.Sprintf("<!-- kelos-merge/v1 -->\n"+`{"approval_id":10,"sha":%q,"version":"2026-11-15T10:00:00Z","invalidated":false}`, p.head))
					body = fmt.Sprintf(`[
						{"id":10,"body":"/lgtm","user":{"id":1,"login":"geoberle"},"created_at":"2026-11-15T10:00:00Z","updated_at":"2026-11-15T10:00:00Z"},
						{"id":11,"body":%s,"user":{"id":90,"login":"controller"},"created_at":"2026-11-15T10:00:05Z","updated_at":"2026-11-15T10:00:05Z"}]`, record)
				case strings.HasSuffix(path, "/comments"), strings.HasSuffix(path, "/reviews"), strings.HasSuffix(path, "/timeline"):
					body = `[]`
				case strings.HasSuffix(path, "/check-runs"):
					body = fmt.Sprintf(`{"check_runs":[{"id":20,"head_sha":%q,"name":"verify","status":"completed","conclusion":"success","app":{"id":7}}]}`, p.head)
				case strings.HasSuffix(path, "/statuses"):
					body = p.statuses
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
			if err := merge.Poll(context.Background(), c, review.NewCache(), []string{"owner/repo"}, "controller"); err != nil {
				mutations = append(mutations, "error: "+err.Error())
			}
			want, err := os.ReadFile("testdata/" + tc.golden + ".golden")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(mutations, "\n"); got != strings.TrimSpace(string(want)) {
				t.Fatalf("writes:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
