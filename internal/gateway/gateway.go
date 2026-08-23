package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
	"github.com/dsri45/gatekeeper/internal/limiter"
)

// Gateway assembles request identification, routing, and backend proxying.
type Gateway struct {
	matcher       *RouteMatcher
	proxies       *ProxyRegistry
	limiter       limiter.Limiter
	failurePolicy string
	metrics       Metrics
}

// Metrics records bounded-label gateway measurements and serves them to Prometheus.
type Metrics interface {
	ObserveRequest(route, result string, elapsed time.Duration)
	IncLimiterError(route string)
	Handler() http.Handler
}

// New validates configuration and creates a ready-to-serve Gateway.
func New(cfg config.Config, requestLimiter limiter.Limiter, applicationMetrics Metrics) (*Gateway, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate gateway configuration: %w", err)
	}
	if requestLimiter == nil {
		return nil, fmt.Errorf("rate limiter is required")
	}
	if applicationMetrics == nil {
		return nil, fmt.Errorf("metrics recorder is required")
	}

	proxies, err := NewProxyRegistry(cfg.Backends)
	if err != nil {
		return nil, fmt.Errorf("prepare backend proxies: %w", err)
	}

	return newGateway(
		NewRouteMatcher(cfg.Routes),
		proxies,
		requestLimiter,
		cfg.Redis.FailurePolicy,
		applicationMetrics,
	), nil
}

func newGateway(
	matcher *RouteMatcher,
	proxies *ProxyRegistry,
	requestLimiter limiter.Limiter,
	failurePolicy string,
	applicationMetrics Metrics,
) *Gateway {
	return &Gateway{
		matcher:       matcher,
		proxies:       proxies,
		limiter:       requestLimiter,
		failurePolicy: failurePolicy,
		metrics:       applicationMetrics,
	}
}

// ServeHTTP handles internal endpoints or proxies a configured application route.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if g.handleInternal(w, request) {
		return
	}

	route, matched := g.matcher.Match(request)
	if !matched {
		writeGatewayError(w, http.StatusNotFound, "route not found")
		return
	}
	started := time.Now()
	result := ""
	defer func() {
		if result != "" {
			g.metrics.ObserveRequest(route.Name(), result, time.Since(started))
		}
	}()

	identity, err := IdentifyClient(request)
	if err != nil {
		writeGatewayError(w, http.StatusBadRequest, "invalid client identity")
		return
	}

	limit := route.LimitFor(identity)
	decision, err := g.limiter.Check(request.Context(), limiter.CheckRequest{
		Route:    route.Name(),
		ClientID: identity.BucketID(),
		Limit:    limit,
	})
	if err != nil {
		g.metrics.IncLimiterError(route.Name())
		if g.failurePolicy == config.FailurePolicyOpen {
			result = "fail_open"
			g.proxy(w, request, route)
			return
		}
		result = "fail_closed"
		writeGatewayError(w, http.StatusServiceUnavailable, "rate limiter unavailable")
		return
	}

	setRateLimitHeaders(w.Header(), limit.Capacity, decision.Remaining)
	if !decision.Allowed {
		result = "rejected"
		w.Header().Set("Retry-After", retryAfterSeconds(decision.RetryAfter))
		writeGatewayError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	result = "allowed"
	g.proxy(w, request, route)
}

func (g *Gateway) proxy(w http.ResponseWriter, request *http.Request, route Route) {
	proxy, found := g.proxies.Handler(route.Backend())
	if !found {
		writeGatewayError(w, http.StatusInternalServerError, "gateway configuration error")
		return
	}

	proxy.ServeHTTP(w, request)
}

func setRateLimitHeaders(header http.Header, capacity, remaining int64) {
	header.Set("RateLimit-Limit", strconv.FormatInt(capacity, 10))
	header.Set("RateLimit-Remaining", strconv.FormatInt(remaining, 10))
}

func retryAfterSeconds(wait time.Duration) string {
	seconds := int64((wait + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(seconds, 10)
}

// CloseIdleConnections releases connections retained by backend transports.
func (g *Gateway) CloseIdleConnections() {
	g.proxies.CloseIdleConnections()
}

func (g *Gateway) handleInternal(w http.ResponseWriter, request *http.Request) bool {
	path := request.URL.Path
	if path == "/health" {
		if request.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeGatewayError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}

		writeGatewayJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
		}{Status: "healthy"})
		return true
	}
	if path == "/metrics" {
		if request.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeGatewayError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}

		g.metrics.Handler().ServeHTTP(w, request)
		return true
	}

	if hasInternalPrefix(path, "/health") ||
		hasInternalPrefix(path, "/ready") ||
		hasInternalPrefix(path, "/metrics") {
		writeGatewayError(w, http.StatusNotFound, "route not found")
		return true
	}

	return false
}

func hasInternalPrefix(path string, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func writeGatewayError(w http.ResponseWriter, status int, message string) {
	writeGatewayJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

func writeGatewayJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
