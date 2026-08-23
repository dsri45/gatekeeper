package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/dsri45/gatekeeper/internal/config"
)

// Gateway assembles request identification, routing, and backend proxying.
type Gateway struct {
	matcher *RouteMatcher
	proxies *ProxyRegistry
}

// New validates configuration and creates a ready-to-serve Gateway.
func New(cfg config.Config) (*Gateway, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate gateway configuration: %w", err)
	}

	proxies, err := NewProxyRegistry(cfg.Backends)
	if err != nil {
		return nil, fmt.Errorf("prepare backend proxies: %w", err)
	}

	return newGateway(NewRouteMatcher(cfg.Routes), proxies), nil
}

func newGateway(matcher *RouteMatcher, proxies *ProxyRegistry) *Gateway {
	return &Gateway{matcher: matcher, proxies: proxies}
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

	if _, err := IdentifyClient(request); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "invalid client identity")
		return
	}

	proxy, found := g.proxies.Handler(route.Backend())
	if !found {
		writeGatewayError(w, http.StatusInternalServerError, "gateway configuration error")
		return
	}

	proxy.ServeHTTP(w, request)
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
