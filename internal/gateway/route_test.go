package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
)

func TestRouteMatcherSelectsLongestPrefixRegardlessOfConfigOrder(t *testing.T) {
	t.Parallel()

	matcher := NewRouteMatcher([]config.RouteConfig{
		testRoute("api", http.MethodGet, "/api", 10),
		testRoute("admin", http.MethodGet, "/api/search/admin", 2),
		testRoute("search", http.MethodGet, "/api/search", 5),
	})

	tests := []struct {
		path     string
		wantName string
	}{
		{path: "/api/items", wantName: "api"},
		{path: "/api/search/results", wantName: "search"},
		{path: "/api/search/admin/stats", wantName: "admin"},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()

			route, ok := matcher.Match(newRequest(http.MethodGet, test.path))
			if !ok {
				t.Fatalf("Match(%q) returned no route", test.path)
			}
			if route.Name() != test.wantName {
				t.Errorf("route name = %q, want %q", route.Name(), test.wantName)
			}
		})
	}
}

func TestRouteMatcherEnforcesPathSegmentBoundaries(t *testing.T) {
	t.Parallel()

	matcher := NewRouteMatcher([]config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 5),
	})

	tests := []struct {
		name      string
		path      string
		wantMatch bool
	}{
		{name: "exact path", path: "/api/search", wantMatch: true},
		{name: "child path", path: "/api/search/results", wantMatch: true},
		{name: "similar segment", path: "/api/searching", wantMatch: false},
		{name: "shorter path", path: "/api", wantMatch: false},
		{name: "case differs", path: "/API/search", wantMatch: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, matched := matcher.Match(newRequest(http.MethodGet, test.path))
			if matched != test.wantMatch {
				t.Errorf("matched = %t, want %t for %q", matched, test.wantMatch, test.path)
			}
		})
	}
}

func TestRouteMatcherHandlesRootAndTrailingSlashPrefixes(t *testing.T) {
	t.Parallel()

	matcher := NewRouteMatcher([]config.RouteConfig{
		testRoute("root", http.MethodGet, "/", 20),
		testRoute("files", http.MethodGet, "/files/", 10),
	})

	tests := []struct {
		path     string
		wantName string
	}{
		{path: "/anything", wantName: "root"},
		{path: "/files/item", wantName: "files"},
		{path: "/files", wantName: "root"},
	}

	for _, test := range tests {
		route, ok := matcher.Match(newRequest(http.MethodGet, test.path))
		if !ok {
			t.Fatalf("Match(%q) returned no route", test.path)
		}
		if route.Name() != test.wantName {
			t.Errorf("route name = %q, want %q", route.Name(), test.wantName)
		}
	}
}

func TestRouteMatcherRequiresExactMethod(t *testing.T) {
	t.Parallel()

	matcher := NewRouteMatcher([]config.RouteConfig{
		testRoute("search-get", http.MethodGet, "/api/search", 10),
		testRoute("search-post", http.MethodPost, "/api/search", 5),
	})

	getRoute, ok := matcher.Match(newRequest(http.MethodGet, "/api/search"))
	if !ok || getRoute.Name() != "search-get" {
		t.Fatalf("GET matched route %q with matched=%t", getRoute.Name(), ok)
	}

	postRoute, ok := matcher.Match(newRequest(http.MethodPost, "/api/search"))
	if !ok || postRoute.Name() != "search-post" {
		t.Fatalf("POST matched route %q with matched=%t", postRoute.Name(), ok)
	}

	if _, ok := matcher.Match(newRequest(http.MethodDelete, "/api/search")); ok {
		t.Error("DELETE unexpectedly matched a route")
	}
}

func TestRouteMatcherIgnoresQueryString(t *testing.T) {
	t.Parallel()

	matcher := NewRouteMatcher([]config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 10),
	})
	route, ok := matcher.Match(newRequest(http.MethodGet, "/api/search?q=redis&page=2"))

	if !ok {
		t.Fatal("request with query string returned no route")
	}
	if route.Name() != "search" {
		t.Errorf("route name = %q, want search", route.Name())
	}
}

func TestRouteMatcherReturnsNoMatchForUnknownPath(t *testing.T) {
	t.Parallel()

	matcher := NewRouteMatcher([]config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 10),
	})
	if _, ok := matcher.Match(newRequest(http.MethodGet, "/api/upload")); ok {
		t.Error("unknown path unexpectedly matched a route")
	}
}

func TestRouteAccessors(t *testing.T) {
	t.Parallel()

	matcher := NewRouteMatcher([]config.RouteConfig{
		{
			Name:       "search",
			Method:     http.MethodGet,
			PathPrefix: "/api/search",
			Backend:    "mock",
			Limit:      testLimit(10),
		},
	})
	route, ok := matcher.Match(newRequest(http.MethodGet, "/api/search"))
	if !ok {
		t.Fatal("Match returned no route")
	}

	if route.Name() != "search" {
		t.Errorf("Name = %q, want search", route.Name())
	}
	if route.Method() != http.MethodGet {
		t.Errorf("Method = %q, want GET", route.Method())
	}
	if route.PathPrefix() != "/api/search" {
		t.Errorf("PathPrefix = %q, want /api/search", route.PathPrefix())
	}
	if route.Backend() != "mock" {
		t.Errorf("Backend = %q, want mock", route.Backend())
	}
}

func TestRouteLimitSelection(t *testing.T) {
	t.Parallel()

	configuredRoute := testRoute("search", http.MethodGet, "/api/search", 50)
	configuredRoute.ClientOverrides = map[string]config.LimitConfig{
		"premium-client": testLimit(100),
	}
	matcher := NewRouteMatcher([]config.RouteConfig{configuredRoute})
	route, _ := matcher.Match(newRequest(http.MethodGet, "/api/search"))

	tests := []struct {
		name         string
		identity     ClientIdentity
		wantCapacity int64
	}{
		{
			name:         "matching API key uses override",
			identity:     identifyWithAPIKey(t, "premium-client"),
			wantCapacity: 100,
		},
		{
			name:         "unknown API key uses default",
			identity:     identifyWithAPIKey(t, "standard-client"),
			wantCapacity: 50,
		},
		{
			name:         "IP identity uses default",
			identity:     identifyFromRemoteAddress(t, "192.0.2.10:5000"),
			wantCapacity: 50,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			limit := route.LimitFor(test.identity)
			if limit.Capacity != test.wantCapacity {
				t.Errorf("capacity = %d, want %d", limit.Capacity, test.wantCapacity)
			}
		})
	}
}

func TestRouteMatcherCopiesConfiguration(t *testing.T) {
	t.Parallel()

	configured := []config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 50),
	}
	configured[0].ClientOverrides = map[string]config.LimitConfig{
		"premium-client": testLimit(100),
	}

	matcher := NewRouteMatcher(configured)
	configured[0].Name = "changed"
	configured[0].PathPrefix = "/changed"
	configured[0].ClientOverrides["premium-client"] = testLimit(999)

	route, ok := matcher.Match(newRequest(http.MethodGet, "/api/search"))
	if !ok {
		t.Fatal("configuration mutation changed the matcher path")
	}
	if route.Name() != "search" {
		t.Errorf("route name = %q, want search", route.Name())
	}
	limit := route.LimitFor(identifyWithAPIKey(t, "premium-client"))
	if limit.Capacity != 100 {
		t.Errorf("override capacity = %d, want 100", limit.Capacity)
	}
}

func TestRouteMatcherSupportsConcurrentReaders(t *testing.T) {
	t.Parallel()

	const readerCount = 250
	matcher := NewRouteMatcher([]config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 10),
	})

	errors := make(chan string, readerCount)
	var waitGroup sync.WaitGroup
	waitGroup.Add(readerCount)

	for range readerCount {
		go func() {
			defer waitGroup.Done()
			route, ok := matcher.Match(newRequest(http.MethodGet, "/api/search/results"))
			if !ok || route.Name() != "search" {
				errors <- "concurrent request returned the wrong route"
			}
		}()
	}

	waitGroup.Wait()
	close(errors)

	for message := range errors {
		t.Error(message)
	}
}

func testRoute(name string, method string, pathPrefix string, capacity int64) config.RouteConfig {
	return config.RouteConfig{
		Name:       name,
		Method:     method,
		PathPrefix: pathPrefix,
		Backend:    "mock",
		Limit:      testLimit(capacity),
	}
}

func testLimit(capacity int64) config.LimitConfig {
	return config.LimitConfig{
		Capacity: capacity,
		Refill: config.RefillConfig{
			Tokens:   capacity,
			Interval: config.NewDuration(time.Minute),
		},
	}
}

func newRequest(method string, target string) *http.Request {
	return httptest.NewRequest(method, target, nil)
}

func identifyWithAPIKey(t *testing.T, apiKey string) ClientIdentity {
	t.Helper()

	request := newRequest(http.MethodGet, "/")
	request.Header.Set(APIKeyHeader, apiKey)
	identity, err := IdentifyClient(request)
	if err != nil {
		t.Fatalf("IdentifyClient returned an error: %v", err)
	}
	return identity
}
