package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dsri45/gatekeeper/internal/config"
)

func TestGatewayProxiesMatchedRequest(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "backend.test" {
			t.Errorf("backend host = %q, want backend.test", request.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"X-Backend":    []string{"mock"},
			},
			Body: io.NopCloser(strings.NewReader(`{"result":"created"}`)),
		}, nil
	})
	gateway := testGateway(t, []config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 10),
	}, testBackends(), transport)

	request := httptest.NewRequest(http.MethodGet, "/api/search?q=redis", nil)
	request.Header.Set(APIKeyHeader, "demo-client")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	if response.Header().Get("X-Backend") != "mock" {
		t.Errorf("X-Backend = %q, want mock", response.Header().Get("X-Backend"))
	}
	if response.Body.String() != `{"result":"created"}` {
		t.Errorf("body = %q", response.Body.String())
	}
}

func TestGatewayRejectsUnknownRouteBeforeProxying(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	gateway := testGateway(t, []config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 10),
	}, testBackends(), countingTransport(&calls))

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "unknown path", method: http.MethodGet, path: "/api/upload"},
		{name: "method mismatch", method: http.MethodPost, path: "/api/search"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))

			if response.Code != http.StatusNotFound {
				t.Errorf("status = %d, want %d", response.Code, http.StatusNotFound)
			}
			if response.Body.String() != "{\"error\":\"route not found\"}\n" {
				t.Errorf("body = %q", response.Body.String())
			}
		})
	}

	if calls.Load() != 0 {
		t.Errorf("backend was called %d times", calls.Load())
	}
}

func TestGatewayRejectsInvalidClientIdentity(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	gateway := testGateway(t, []config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 10),
	}, testBackends(), countingTransport(&calls))

	request := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	request.Header.Add(APIKeyHeader, "client-a")
	request.Header.Add(APIKeyHeader, "client-b")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if response.Body.String() != "{\"error\":\"invalid client identity\"}\n" {
		t.Errorf("body = %q", response.Body.String())
	}
	if calls.Load() != 0 {
		t.Errorf("backend was called %d times", calls.Load())
	}
}

func TestGatewayHandlesMissingPreparedBackend(t *testing.T) {
	t.Parallel()

	matcher := NewRouteMatcher([]config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 10),
	})
	proxies := mustProxyRegistry(t, map[string]config.BackendConfig{
		"different": {URL: "http://backend.test"},
	}, countingTransport(new(atomic.Int64)))
	gateway := newGateway(matcher, proxies)

	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/search", nil))

	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if response.Body.String() != "{\"error\":\"gateway configuration error\"}\n" {
		t.Errorf("body = %q", response.Body.String())
	}
}

func TestGatewayHealthAndReservedPathsNeverReachBackend(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	gateway := testGateway(t, []config.RouteConfig{
		testRoute("catch-all", http.MethodGet, "/", 10),
	}, testBackends(), countingTransport(&calls))

	health := httptest.NewRecorder()
	gateway.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Errorf("health status = %d, want %d", health.Code, http.StatusOK)
	}
	if health.Body.String() != "{\"status\":\"healthy\"}\n" {
		t.Errorf("health body = %q", health.Body.String())
	}

	for _, path := range []string{"/health/details", "/ready", "/ready/details", "/metrics", "/metrics/details"} {
		response := httptest.NewRecorder()
		gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
	}

	if calls.Load() != 0 {
		t.Errorf("backend was called %d times", calls.Load())
	}
}

func TestGatewayHealthRequiresGET(t *testing.T) {
	t.Parallel()

	gateway := testGateway(t, []config.RouteConfig{
		testRoute("catch-all", http.MethodPost, "/", 10),
	}, testBackends(), countingTransport(new(atomic.Int64)))
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/health", nil))

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if response.Header().Get("Allow") != http.MethodGet {
		t.Errorf("Allow = %q, want GET", response.Header().Get("Allow"))
	}
}

func TestGatewaySupportsConcurrentRequests(t *testing.T) {
	t.Parallel()

	const requestCount = 250
	var calls atomic.Int64
	gateway := testGateway(t, []config.RouteConfig{
		testRoute("search", http.MethodGet, "/api/search", 10),
	}, testBackends(), countingTransport(&calls))

	var waitGroup sync.WaitGroup
	waitGroup.Add(requestCount)
	for range requestCount {
		go func() {
			defer waitGroup.Done()
			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/search", nil))
			if response.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", response.Code, http.StatusOK)
			}
		}()
	}
	waitGroup.Wait()

	if calls.Load() != requestCount {
		t.Errorf("backend calls = %d, want %d", calls.Load(), requestCount)
	}
}

func testGateway(
	t *testing.T,
	routes []config.RouteConfig,
	backends map[string]config.BackendConfig,
	transport http.RoundTripper,
) *Gateway {
	t.Helper()

	registry := mustProxyRegistry(t, backends, transport)
	return newGateway(NewRouteMatcher(routes), registry)
}

func countingTransport(calls *atomic.Int64) http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return successfulResponse(), nil
	})
}
