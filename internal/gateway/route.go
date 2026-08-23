package gateway

import (
	"net/http"
	"sort"
	"strings"

	"github.com/dsri45/gatekeeper/internal/config"
)

// RouteMatcher selects the most specific configured route for a request.
// Its state is immutable after construction and safe for concurrent readers.
type RouteMatcher struct {
	routes []Route
}

// Route is an immutable runtime copy of a configured gateway route.
type Route struct {
	name            string
	method          string
	pathPrefix      string
	backend         string
	defaultLimit    config.LimitConfig
	clientOverrides map[string]config.LimitConfig
}

// NewRouteMatcher copies and sorts routes from longest prefix to shortest.
func NewRouteMatcher(configured []config.RouteConfig) *RouteMatcher {
	routes := make([]Route, len(configured))
	for index, configuredRoute := range configured {
		overrides := make(map[string]config.LimitConfig, len(configuredRoute.ClientOverrides))
		for apiKey, limit := range configuredRoute.ClientOverrides {
			overrides[apiKey] = limit
		}

		routes[index] = Route{
			name:            configuredRoute.Name,
			method:          configuredRoute.Method,
			pathPrefix:      configuredRoute.PathPrefix,
			backend:         configuredRoute.Backend,
			defaultLimit:    configuredRoute.Limit,
			clientOverrides: overrides,
		}
	}

	sort.SliceStable(routes, func(first, second int) bool {
		return len(routes[first].pathPrefix) > len(routes[second].pathPrefix)
	})

	return &RouteMatcher{routes: routes}
}

// Match returns the most specific route matching the request method and path.
func (m *RouteMatcher) Match(request *http.Request) (Route, bool) {
	method := strings.ToUpper(request.Method)
	path := request.URL.Path

	for _, route := range m.routes {
		if route.method == method && matchesPathPrefix(path, route.pathPrefix) {
			return route, true
		}
	}

	return Route{}, false
}

// Name returns the stable configured route name.
func (r Route) Name() string {
	return r.name
}

// Method returns the route's configured HTTP method.
func (r Route) Method() string {
	return r.method
}

// PathPrefix returns the route's configured path prefix.
func (r Route) PathPrefix() string {
	return r.pathPrefix
}

// Backend returns the configured backend name.
func (r Route) Backend() string {
	return r.backend
}

// LimitFor returns an API-key override when present, otherwise the default.
func (r Route) LimitFor(identity ClientIdentity) config.LimitConfig {
	if apiKey, ok := identity.APIKey(); ok {
		if limit, exists := r.clientOverrides[apiKey]; exists {
			return limit
		}
	}
	return r.defaultLimit
}

func matchesPathPrefix(path string, prefix string) bool {
	if path == prefix {
		return true
	}
	if prefix == "/" {
		return strings.HasPrefix(path, "/")
	}
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(path, prefix)
	}
	if !strings.HasPrefix(path, prefix) || len(path) <= len(prefix) {
		return false
	}
	return path[len(prefix)] == '/'
}
