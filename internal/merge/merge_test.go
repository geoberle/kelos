package merge_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v80/github"

	merge "github.com/hearthctl/kelos-orchestrator/internal/merge"
	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

func TestUnavailableDiscoveryNeverMutates(t *testing.T) {
	for _, status := range []int{403, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("unexpected mutation %s", r.Method)
				}
				w.WriteHeader(status)
			}))
			defer s.Close()
			c := github.NewClient(s.Client())
			c.BaseURL, _ = url.Parse(s.URL + "/")
			if err := merge.Poll(context.Background(), c, review.NewCache(), []string{"hearthctl/kelos-orchestrator"}, "controller"); err == nil {
				t.Fatal("expected unavailable evidence error")
			}
		})
	}
}
