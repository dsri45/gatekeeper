package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	readiness     ReadinessChecker
	failurePolicy string
	metrics       Metrics
	logger        *slog.Logger
}

// ReadinessChecker verifies whether the rate-limit dependency is available.
type ReadinessChecker interface {
	Ping(context.Context) error
}

// Metrics records bounded-label gateway measurements and serves them to Prometheus.
type Metrics interface {
	ObserveRequest(route, result string, elapsed time.Duration)
	IncLimiterError(route string)
	Handler() http.Handler
}

// New validates configuration and creates a ready-to-serve Gateway.
func New(
	cfg config.Config,
	requestLimiter limiter.Limiter,
	readiness ReadinessChecker,
	applicationMetrics Metrics,
	logger *slog.Logger,
) (*Gateway, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate gateway configuration: %w", err)
	}
	if requestLimiter == nil {
		return nil, fmt.Errorf("rate limiter is required")
	}
	if readiness == nil {
		return nil, fmt.Errorf("readiness checker is required")
	}
	if applicationMetrics == nil {
		return nil, fmt.Errorf("metrics recorder is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	proxies, err := NewProxyRegistry(cfg.Backends)
	if err != nil {
		return nil, fmt.Errorf("prepare backend proxies: %w", err)
	}

	return newGateway(
		NewRouteMatcher(cfg.Routes),
		proxies,
		requestLimiter,
		readiness,
		cfg.Redis.FailurePolicy,
		applicationMetrics,
		logger,
	), nil
}

func newGateway(
	matcher *RouteMatcher,
	proxies *ProxyRegistry,
	requestLimiter limiter.Limiter,
	readiness ReadinessChecker,
	failurePolicy string,
	applicationMetrics Metrics,
	logger *slog.Logger,
) *Gateway {
	return &Gateway{
		matcher:       matcher,
		proxies:       proxies,
		limiter:       requestLimiter,
		readiness:     readiness,
		failurePolicy: failurePolicy,
		metrics:       applicationMetrics,
		logger:        logger,
	}
}

// ServeHTTP handles internal endpoints or proxies a configured application route.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	recorder := &statusRecorder{ResponseWriter: w}
	w = recorder
	started := time.Now()
	logFields := requestLog{route: "unmatched", result: "route_not_found"}
	defer func() {
		g.logRequest(request, recorder.statusCode(), time.Since(started), logFields)
	}()

	if internalRoute, handled := g.handleInternal(recorder, request); handled {
		logFields.route = internalRoute
		logFields.result = "internal"
		return
	}

	route, matched := g.matcher.Match(request)
	if !matched {
		writeGatewayError(w, http.StatusNotFound, "route not found")
		return
	}
	logFields.route = route.Name()
	logFields.backend = route.Backend()
	metricsStarted := time.Now()
	result := ""
	defer func() {
		if result != "" {
			g.metrics.ObserveRequest(route.Name(), result, time.Since(metricsStarted))
		}
	}()

	identity, err := IdentifyClient(request)
	if err != nil {
		logFields.result = "invalid_client"
		writeGatewayError(w, http.StatusBadRequest, "invalid client identity")
		return
	}
	logFields.clientKind = string(identity.Kind())

	limit := route.LimitFor(identity)
	decision, err := g.limiter.Check(request.Context(), limiter.CheckRequest{
		Route:    route.Name(),
		ClientID: identity.BucketID(),
		Limit:    limit,
	})
	if err != nil {
		logFields.redisError = true
		g.metrics.IncLimiterError(route.Name())
		if g.failurePolicy == config.FailurePolicyOpen {
			result = "fail_open"
			logFields.result = result
			g.proxy(w, request, route)
			return
		}
		result = "fail_closed"
		logFields.result = result
		writeGatewayError(w, http.StatusServiceUnavailable, "rate limiter unavailable")
		return
	}

	setRateLimitHeaders(w.Header(), limit.Capacity, decision.Remaining)
	if !decision.Allowed {
		result = "rejected"
		logFields.result = result
		w.Header().Set("Retry-After", retryAfterSeconds(decision.RetryAfter))
		writeGatewayError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	result = "allowed"
	logFields.result = result
	g.proxy(w, request, route)
}

type requestLog struct {
	route      string
	backend    string
	result     string
	clientKind string
	redisError bool
}

func (g *Gateway) logRequest(
	request *http.Request,
	status int,
	elapsed time.Duration,
	fields requestLog,
) {
	attributes := []any{
		"method", request.Method,
		"route", fields.route,
		"result", fields.result,
		"status", status,
		"duration_ms", float64(elapsed.Microseconds()) / 1000,
	}
	if fields.backend != "" {
		attributes = append(attributes, "backend", fields.backend)
	}
	if fields.clientKind != "" {
		attributes = append(attributes, "client_kind", fields.clientKind)
	}
	if fields.redisError {
		attributes = append(attributes, "redis_error", true)
	}

	if status >= http.StatusInternalServerError || fields.redisError {
		g.logger.ErrorContext(request.Context(), "request completed", attributes...)
		return
	}
	g.logger.InfoContext(request.Context(), "request completed", attributes...)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *statusRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
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

func (g *Gateway) handleInternal(w http.ResponseWriter, request *http.Request) (string, bool) {
	path := request.URL.Path
	if path == "/health" {
		if request.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeGatewayError(w, http.StatusMethodNotAllowed, "method not allowed")
			return "health", true
		}

		writeGatewayJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
		}{Status: "healthy"})
		return "health", true
	}
	if path == "/ready" {
		if request.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeGatewayError(w, http.StatusMethodNotAllowed, "method not allowed")
			return "ready", true
		}

		if err := g.readiness.Ping(request.Context()); err == nil {
			writeReadiness(w, http.StatusOK, "ready", "available")
		} else if g.failurePolicy == config.FailurePolicyOpen {
			writeReadiness(w, http.StatusOK, "degraded", "unavailable")
		} else {
			writeReadiness(w, http.StatusServiceUnavailable, "not_ready", "unavailable")
		}
		return "ready", true
	}
	if path == "/metrics" {
		if request.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeGatewayError(w, http.StatusMethodNotAllowed, "method not allowed")
			return "metrics", true
		}

		g.metrics.Handler().ServeHTTP(w, request)
		return "metrics", true
	}

	if hasInternalPrefix(path, "/health") ||
		hasInternalPrefix(path, "/ready") ||
		hasInternalPrefix(path, "/metrics") {
		writeGatewayError(w, http.StatusNotFound, "route not found")
		return "unmatched", true
	}

	return "", false
}

func writeReadiness(w http.ResponseWriter, status int, state, redisState string) {
	writeGatewayJSON(w, status, struct {
		Status string `json:"status"`
		Redis  string `json:"redis"`
	}{Status: state, Redis: redisState})
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
