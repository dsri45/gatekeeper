package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
	"github.com/dsri45/gatekeeper/internal/limiter"
)

type fakeLimiter struct {
	mu       sync.Mutex
	decision limiter.Decision
	err      error
	requests []limiter.CheckRequest
}

type metricObservation struct {
	route  string
	result string
}

type fakeMetrics struct {
	mu            sync.Mutex
	observations  []metricObservation
	limiterErrors []string
}

func (f *fakeMetrics) ObserveRequest(route, result string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations = append(f.observations, metricObservation{route: route, result: result})
}

func (f *fakeMetrics) IncLimiterError(route string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limiterErrors = append(f.limiterErrors, route)
}

func (f *fakeMetrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("test_metric 1\n"))
	})
}

func (f *fakeMetrics) lastObservation() metricObservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.observations[len(f.observations)-1]
}

func (f *fakeLimiter) Check(_ context.Context, request limiter.CheckRequest) (limiter.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	return f.decision, f.err
}

func (f *fakeLimiter) lastRequest() limiter.CheckRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

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
	gateway := newGateway(
		matcher,
		proxies,
		&fakeLimiter{decision: limiter.Decision{Allowed: true}},
		config.FailurePolicyOpen,
		&fakeMetrics{},
	)

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

	for _, path := range []string{"/health/details", "/ready", "/ready/details", "/metrics/details"} {
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

func TestGatewayRejectsLimitedRequestBeforeProxying(t *testing.T) {
	t.Parallel()

	var backendCalls atomic.Int64
	requestLimiter := &fakeLimiter{decision: limiter.Decision{
		Allowed:    false,
		Remaining:  0,
		RetryAfter: 250 * time.Millisecond,
	}}
	gateway := testGatewayWithLimiter(
		t,
		[]config.RouteConfig{testRoute("search", http.MethodGet, "/api/search", 10)},
		testBackends(),
		countingTransport(&backendCalls),
		requestLimiter,
		config.FailurePolicyOpen,
	)

	request := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	request.Header.Set(APIKeyHeader, "demo-client")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if response.Header().Get("RateLimit-Limit") != "10" {
		t.Errorf("RateLimit-Limit = %q, want 10", response.Header().Get("RateLimit-Limit"))
	}
	if response.Header().Get("RateLimit-Remaining") != "0" {
		t.Errorf("RateLimit-Remaining = %q, want 0", response.Header().Get("RateLimit-Remaining"))
	}
	if response.Header().Get("Retry-After") != "1" {
		t.Errorf("Retry-After = %q, want 1", response.Header().Get("Retry-After"))
	}
	if response.Body.String() != "{\"error\":\"rate limit exceeded\"}\n" {
		t.Errorf("body = %q", response.Body.String())
	}
	if backendCalls.Load() != 0 {
		t.Errorf("backend was called %d times", backendCalls.Load())
	}
}

func TestGatewayUsesClientOverride(t *testing.T) {
	t.Parallel()

	route := testRoute("search", http.MethodGet, "/api/search", 10)
	route.ClientOverrides = map[string]config.LimitConfig{
		"premium-client": {
			Capacity: 100,
			Refill: config.RefillConfig{
				Tokens:   100,
				Interval: config.NewDuration(time.Minute),
			},
		},
	}
	requestLimiter := &fakeLimiter{decision: limiter.Decision{Allowed: true, Remaining: 99}}
	gateway := testGatewayWithLimiter(
		t,
		[]config.RouteConfig{route},
		testBackends(),
		countingTransport(new(atomic.Int64)),
		requestLimiter,
		config.FailurePolicyOpen,
	)

	request := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	request.Header.Set(APIKeyHeader, "premium-client")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Header().Get("RateLimit-Limit") != "100" {
		t.Errorf("RateLimit-Limit = %q, want 100", response.Header().Get("RateLimit-Limit"))
	}
	if got := requestLimiter.lastRequest().Limit.Capacity; got != 100 {
		t.Errorf("limiter capacity = %d, want 100", got)
	}
}

func TestGatewayRedisFailurePolicies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		policy           string
		wantStatus       int
		wantBackendCalls int64
	}{
		{name: "fail open", policy: config.FailurePolicyOpen, wantStatus: http.StatusOK, wantBackendCalls: 1},
		{name: "fail closed", policy: config.FailurePolicyClosed, wantStatus: http.StatusServiceUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var backendCalls atomic.Int64
			gateway := testGatewayWithLimiter(
				t,
				[]config.RouteConfig{testRoute("search", http.MethodGet, "/api/search", 10)},
				testBackends(),
				countingTransport(&backendCalls),
				&fakeLimiter{err: errors.New("Redis unavailable")},
				test.policy,
			)

			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/search", nil))

			if response.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if backendCalls.Load() != test.wantBackendCalls {
				t.Errorf("backend calls = %d, want %d", backendCalls.Load(), test.wantBackendCalls)
			}
		})
	}
}

func TestGatewayRecordsRequestOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		decision limiter.Decision
		err      error
		policy   string
		want     string
	}{
		{name: "allowed", decision: limiter.Decision{Allowed: true}, policy: config.FailurePolicyOpen, want: "allowed"},
		{name: "rejected", decision: limiter.Decision{}, policy: config.FailurePolicyOpen, want: "rejected"},
		{name: "fail open", err: errors.New("unavailable"), policy: config.FailurePolicyOpen, want: "fail_open"},
		{name: "fail closed", err: errors.New("unavailable"), policy: config.FailurePolicyClosed, want: "fail_closed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			applicationMetrics := &fakeMetrics{}
			gateway := testGatewayWithDependencies(
				t,
				[]config.RouteConfig{testRoute("search", http.MethodGet, "/api/search", 10)},
				testBackends(),
				countingTransport(new(atomic.Int64)),
				&fakeLimiter{decision: test.decision, err: test.err},
				test.policy,
				applicationMetrics,
			)

			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/search", nil))

			observation := applicationMetrics.lastObservation()
			if observation.route != "search" || observation.result != test.want {
				t.Errorf("observation = %#v, want route search and result %s", observation, test.want)
			}
			if test.err != nil && len(applicationMetrics.limiterErrors) != 1 {
				t.Errorf("limiter error count = %d, want 1", len(applicationMetrics.limiterErrors))
			}
		})
	}
}

func TestGatewayMetricsEndpointBypassesLimiter(t *testing.T) {
	t.Parallel()

	requestLimiter := &fakeLimiter{decision: limiter.Decision{Allowed: true}}
	applicationMetrics := &fakeMetrics{}
	gateway := testGatewayWithDependencies(
		t,
		[]config.RouteConfig{testRoute("catch-all", http.MethodGet, "/", 10)},
		testBackends(),
		countingTransport(new(atomic.Int64)),
		requestLimiter,
		config.FailurePolicyOpen,
		applicationMetrics,
	)

	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if response.Code != http.StatusOK || response.Body.String() != "test_metric 1\n" {
		t.Errorf("metrics response = status %d body %q", response.Code, response.Body.String())
	}
	if len(requestLimiter.requests) != 0 {
		t.Errorf("limiter received %d requests, want 0", len(requestLimiter.requests))
	}
	if len(applicationMetrics.observations) != 0 {
		t.Errorf("application observations = %d, want 0", len(applicationMetrics.observations))
	}

	methodNotAllowed := httptest.NewRecorder()
	gateway.ServeHTTP(methodNotAllowed, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if methodNotAllowed.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics status = %d, want %d", methodNotAllowed.Code, http.StatusMethodNotAllowed)
	}
}

func TestRetryAfterSecondsRoundsUp(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		wait time.Duration
		want string
	}{
		{wait: 0, want: "1"},
		{wait: time.Millisecond, want: "1"},
		{wait: time.Second, want: "1"},
		{wait: time.Second + time.Millisecond, want: "2"},
	} {
		if got := retryAfterSeconds(test.wait); got != test.want {
			t.Errorf("retryAfterSeconds(%s) = %q, want %q", test.wait, got, test.want)
		}
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
	return newGateway(
		NewRouteMatcher(routes),
		registry,
		&fakeLimiter{decision: limiter.Decision{Allowed: true}},
		config.FailurePolicyOpen,
		&fakeMetrics{},
	)
}

func testGatewayWithLimiter(
	t *testing.T,
	routes []config.RouteConfig,
	backends map[string]config.BackendConfig,
	transport http.RoundTripper,
	requestLimiter limiter.Limiter,
	failurePolicy string,
) *Gateway {
	t.Helper()

	registry := mustProxyRegistry(t, backends, transport)
	return newGateway(
		NewRouteMatcher(routes),
		registry,
		requestLimiter,
		failurePolicy,
		&fakeMetrics{},
	)
}

func testGatewayWithDependencies(
	t *testing.T,
	routes []config.RouteConfig,
	backends map[string]config.BackendConfig,
	transport http.RoundTripper,
	requestLimiter limiter.Limiter,
	failurePolicy string,
	applicationMetrics Metrics,
) *Gateway {
	t.Helper()

	registry := mustProxyRegistry(t, backends, transport)
	return newGateway(
		NewRouteMatcher(routes),
		registry,
		requestLimiter,
		failurePolicy,
		applicationMetrics,
	)
}

func countingTransport(calls *atomic.Int64) http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return successfulResponse(), nil
	})
}
