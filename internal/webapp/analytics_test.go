// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package webapp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ostendorf-cloud/govue/analytics"
	"github.com/ostendorf-cloud/govue/clientip"

	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/ownertest"
)

const (
	umamiID   = "0a1b2c3d-0000-4000-8000-000000000001"
	visitorUA = "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"
)

// fakeUmami is the transport standing in for Umami, so no socket is opened.
// When block is set, every post waits on it, which stands in for a Umami that
// never answers.
type fakeUmami struct {
	block chan struct{}

	mu    sync.Mutex
	posts []string
}

func (f *fakeUmami) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	body, _ := io.ReadAll(req.Body)
	f.mu.Lock()
	f.posts = append(f.posts, req.URL.String()+" "+string(body))
	f.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func (f *fakeUmami) bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.posts)
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func countingFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte("<html>spa</html>")},
		"assets/app.js":         {Data: []byte("console.log(1)")},
		"en/privacy/index.html": {Data: []byte("<html>privacy</html>")},
		"de/imprint/index.html": {Data: []byte("<html>imprint</html>")},
		"de/privacy/index.html": {Data: []byte("<html>datenschutz</html>")},
		"en/imprint/index.html": {Data: []byte("<html>imprint</html>")},
		"sitemap.xml":           {Data: []byte("<urlset/>")},
	}
}

// countingHandler builds the real public handler with counting on, posting to
// fake, with a signed in owner so the API answers JSON.
func countingHandler(t *testing.T, fake *fakeUmami, trusted []netip.Prefix) (http.Handler, *analytics.Client) {
	t.Helper()

	ips := clientip.NewResolver(trusted)
	views, err := NewViews(analytics.Config{URL: "https://umami.example.test", WebsiteID: umamiID},
		"my-wishes.example.test", ips, discard(), &http.Client{Transport: fake}, nil)
	if err != nil {
		t.Fatalf("NewViews: %v", err)
	}
	h, err := Handler(Config{
		Store:     ownertest.New(),
		Owner:     func(*http.Request) (int64, bool) { return 1, true },
		Dist:      countingFS(),
		ClientIPs: ips,
		Views:     views,
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	return h, views
}

func visit(t *testing.T, h http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	req.RemoteAddr = "203.0.113.7:4000"
	req.Header.Set("User-Agent", visitorUA)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func closeViews(t *testing.T, views *analytics.Client) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := views.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func payloadOf(t *testing.T, post string) map[string]any {
	t.Helper()

	_, body, _ := strings.Cut(post, " ")
	var ev struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(body), &ev); err != nil {
		t.Fatal(err)
	}
	return ev.Payload
}

// A guest's reservation and share routes carry a token or an id in the path.
// Reservation blindness needs that none of it reaches Umami: only the route
// pattern is reported. The owner's deep links stand in for those paths here,
// since they take the same route through the SPA fallback, and the secrets
// are also tried in the query.
func TestCountingReportsTheRoutePatternAndNeverAnIdATokenOrAQuery(t *testing.T) {
	t.Parallel()

	fake := &fakeUmami{}
	h, views := countingHandler(t, fake, nil)

	needles := []string{"needle-share", "needle-reserve", "needle-extra", "4242"}
	targets := []string{
		"/lists/4242/edit?a=needle-share&b=needle-reserve",
		"/wishes/4242?c=needle-extra",
		"/lists/needle-share",
		"/en/privacy?a=needle-share",
	}
	want := []string{"/", "/", "/", "/en/privacy"}
	for _, target := range targets {
		if rec := visit(t, h, http.MethodGet, target, http.Header{"Referer": {"https://other.example.test/p/needle-share?t=needle-extra"}}); rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", target, rec.Code)
		}
	}
	closeViews(t, views)

	posts := fake.bodies()
	if len(posts) != len(targets) {
		t.Fatalf("got %d views, want %d: %v", len(posts), len(targets), posts)
	}
	var urls []string
	for _, post := range posts {
		u, _ := payloadOf(t, post)["url"].(string)
		urls = append(urls, u)
		for _, needle := range needles {
			if strings.Contains(post, needle) {
				t.Errorf("a post holds %q: %s", needle, post)
			}
		}
		_, body, _ := strings.Cut(post, " ")
		if strings.Contains(body, "?") {
			t.Errorf("a payload holds a query string: %s", body)
		}
	}
	slices.Sort(urls)
	wantSorted := slices.Clone(want)
	slices.Sort(wantSorted)
	if !slices.Equal(urls, wantSorted) {
		t.Errorf("reported urls = %v, want %v", urls, wantSorted)
	}
}

// Only HTML pages that matched a route pattern are counted. The owner API,
// built files, misses, redirects, other methods, prefetch, Do Not Track,
// Global Privacy Control and bots count nothing, and a path with no route of
// its own is not echoed.
func TestCountingCountsOnlyPagesThroughTheRealChain(t *testing.T) {
	t.Parallel()

	fake := &fakeUmami{}
	h, views := countingHandler(t, fake, nil)

	visit(t, h, http.MethodGet, "/", nil)                                           // counted
	visit(t, h, http.MethodGet, "/", http.Header{"Dnt": {"1"}})                     // Do Not Track
	visit(t, h, http.MethodGet, "/", http.Header{"Sec-Gpc": {"1"}})                 // Global Privacy Control
	visit(t, h, http.MethodGet, "/", http.Header{"Sec-Purpose": {"prefetch"}})      // prefetch
	visit(t, h, http.MethodGet, "/", http.Header{"User-Agent": {"Googlebot/2.1"}})  // bot
	visit(t, h, http.MethodHead, "/", nil)                                          // not a GET
	visit(t, h, http.MethodGet, owner.Prefix+"/lists", nil)                         // API, JSON
	visit(t, h, http.MethodGet, owner.Prefix+"/lists/4242", nil)                    // API with an id
	visit(t, h, http.MethodGet, "/api/nothing", nil)                                // API miss
	visit(t, h, http.MethodGet, "/assets/app.js", nil)                              // asset
	visit(t, h, http.MethodGet, "/sitemap.xml", nil)                                // file, not HTML
	visit(t, h, http.MethodGet, "/s/SHARETOKEN123", nil)                            // no such page
	visit(t, h, http.MethodGet, "/index.html", nil)                                 // redirect
	visit(t, h, http.MethodGet, "/privacy", http.Header{"Accept-Language": {"en"}}) // redirect
	visit(t, h, http.MethodGet, "/impressum", nil)                                  // redirect
	closeViews(t, views)

	posts := fake.bodies()
	if len(posts) != 1 {
		t.Fatalf("got %d views, want 1: %v", len(posts), posts)
	}
	if got := payloadOf(t, posts[0])["url"]; got != "/" {
		t.Errorf("reported url = %v, want /", got)
	}
	for _, leak := range []string{"SHARETOKEN123", "4242", "nothing"} {
		if strings.Contains(posts[0], leak) {
			t.Errorf("payload holds %q: %s", leak, posts[0])
		}
	}
}

// The payload holds the fields the privacy text states and nothing else.
func TestCountingPayloadHoldsOnlyTheStatedFields(t *testing.T) {
	t.Parallel()

	fake := &fakeUmami{}
	h, views := countingHandler(t, fake, nil)
	visit(t, h, http.MethodGet, "/lists/1?x=y", http.Header{
		"Accept-Language": {"de-DE,de;q=0.9"},
		"Cookie":          {"session=SECRETCOOKIE"},
		"Authorization":   {"Bearer SECRETBEARER"},
	})
	closeViews(t, views)

	posts := fake.bodies()
	if len(posts) != 1 {
		t.Fatalf("got %d views, want 1", len(posts))
	}
	for _, leak := range []string{"SECRETCOOKIE", "SECRETBEARER"} {
		if strings.Contains(posts[0], leak) {
			t.Errorf("post holds %q: %s", leak, posts[0])
		}
	}
	got := payloadOf(t, posts[0])
	for key := range got {
		if !slices.Contains([]string{"website", "hostname", "url", "language", "ip", "userAgent"}, key) {
			t.Errorf("payload has the unexpected field %q", key)
		}
	}
	if got["hostname"] != "my-wishes.example.test" {
		t.Errorf("hostname = %v, want the configured site host", got["hostname"])
	}
}

// A Umami that never answers must not delay a response.
func TestASlowUmamiNeverDelaysAResponse(t *testing.T) {
	t.Parallel()

	fake := &fakeUmami{block: make(chan struct{})}
	h, views := countingHandler(t, fake, nil)
	t.Cleanup(func() {
		close(fake.block)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = views.Close(ctx)
	})

	start := time.Now()
	for range 20 {
		if rec := visit(t, h, http.MethodGet, "/", nil); rec.Code != http.StatusOK {
			t.Fatalf("GET / = %d, want 200", rec.Code)
		}
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("20 responses took %v with Umami blocked, want no wait", took)
	}
}

// The address Umami receives is the masked client address: the forwarded
// client behind a trusted proxy, the TCP peer when the peer is not trusted
// (a forged X-Forwarded-For is ignored), and never the whole address.
func TestCountingMasksTheAddressAndTrustsOnlyTheConfiguredProxies(t *testing.T) {
	t.Parallel()

	proxy := []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	cases := []struct {
		name    string
		trusted []netip.Prefix
		peer    string
		xff     string
		wantIP  string
	}{
		{name: "no proxy trusted, peer is the client", peer: "203.0.113.77:4000", wantIP: "203.0.113.0"},
		{name: "trusted proxy, forwarded client", trusted: proxy, peer: "198.51.100.9:4000", xff: "192.0.2.200", wantIP: "192.0.2.0"},
		{name: "untrusted peer, forged header ignored", trusted: proxy, peer: "203.0.113.77:4000", xff: "192.0.2.200", wantIP: "203.0.113.0"},
		{name: "IPv6 is masked to its /48", peer: "[2001:db8:1234:5678::9]:4000", wantIP: "2001:db8:1234::"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeUmami{}
			h, views := countingHandler(t, fake, tc.trusted)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			req.RemoteAddr = tc.peer
			req.Header.Set("User-Agent", visitorUA)
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			closeViews(t, views)

			posts := fake.bodies()
			if len(posts) != 1 {
				t.Fatalf("got %d views, want 1: %v", len(posts), posts)
			}
			if got := payloadOf(t, posts[0])["ip"]; got != tc.wantIP {
				t.Errorf("reported ip = %v, want %s", got, tc.wantIP)
			}
		})
	}
}

// An unset pair is inert, and so is a handler built without views.
func TestCountingOffCountsNothing(t *testing.T) {
	t.Parallel()

	views, err := NewViews(analytics.Config{}, "", nil, discard(), nil, nil)
	if err != nil {
		t.Fatalf("NewViews: %v", err)
	}
	if views.Enabled() {
		t.Fatal("an empty configuration must not count")
	}
	h, err := Handler(Config{
		Store: ownertest.New(),
		Owner: func(*http.Request) (int64, bool) { return 1, true },
		Dist:  countingFS(),
		Views: views,
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if rec := visit(t, h, http.MethodGet, "/", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if got := views.Stats(); got.Sent+got.Failed+got.Dropped != 0 {
		t.Errorf("stats = %+v, nothing may be queued", got)
	}
}
