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
}

// New validates configuration and creates a ready-to-serve Gateway.
func New(cfg config.Config, requestLimiter limiter.Limiter) (*Gateway, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate gateway configuration: %w", err)
	}
	if requestLimiter == nil {
		return nil, fmt.Errorf("rate limiter is required")
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
	), nil
}

func newGateway(
	matcher *RouteMatcher,
	proxies *ProxyRegistry,
	requestLimiter limiter.Limiter,
	failurePolicy string,
) *Gateway {
	return &Gateway{
		matcher:       matcher,
		proxies:       proxies,
		limiter:       requestLimiter,
		failurePolicy: failurePolicy,
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
		if g.failurePolicy == config.FailurePolicyOpen {
			g.proxy(w, request, route)
			return
		}
		writeGatewayError(w, http.StatusServiceUnavailable, "rate limiter unavailable")
		return
	}

	setRateLimitHeaders(w.Header(), limit.Capacity, decision.Remaining)
	if !decision.Allowed {
		w.Header().Set("Retry-After", retryAfterSeconds(decision.RetryAfter))
		writeGatewayError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

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
