package options

import (
	"net/http"
	"sync"
	"time"

	"github.com/gregjones/httpcache"
)

// newHTTPClient returns a client that sends every repeated read as a
// conditional request. GitHub answers an unchanged resource with 304, which
// does not count against the rate limit. base performs the authenticated
// request.
func newHTTPClient(base http.RoundTripper) (*http.Client, *meter) {
	m := &meter{base: base, cache: httpcache.NewMemoryCacheTransport()}
	m.cache.Transport = m
	return &http.Client{Transport: revalidate{m.cache}, Timeout: 30 * time.Second}, m
}

// revalidate makes the cache ask GitHub on every use. GitHub permits reusing
// a response for a minute, but the controller reads back what it just wrote
// and must never act on a stale answer.
type revalidate struct{ next http.RoundTripper }

func (r revalidate) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Cache-Control", "max-age=0")
	return r.next.RoundTrip(request)
}

// meter counts what one poll costs and owns the response cache.
type meter struct {
	base  http.RoundTripper
	cache *httpcache.Transport

	lock        sync.Mutex
	requests    int
	notModified int
	remaining   string
}

func (m *meter) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := m.base.RoundTrip(request)
	m.lock.Lock()
	defer m.lock.Unlock()
	m.requests++
	if response != nil {
		if response.StatusCode == http.StatusNotModified {
			m.notModified++
		}
		if remaining := response.Header.Get("X-RateLimit-Remaining"); len(remaining) > 0 {
			m.remaining = remaining
		}
	}
	return response, err
}

// reset returns the requests sent and 304 answers received since the last
// call, and the rate limit GitHub last reported as remaining.
func (m *meter) reset() (requests, notModified int, remaining string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	requests, notModified = m.requests, m.notModified
	m.requests, m.notModified = 0, 0
	return requests, notModified, m.remaining
}

// forget drops every cached response. The cache has no eviction, so it would
// otherwise keep answers for closed pull requests and old runs forever. The
// next read of each resource counts against the rate limit once. It must not
// be called while a request is in flight.
func (m *meter) forget() {
	m.cache.Cache = httpcache.NewMemoryCache()
}
