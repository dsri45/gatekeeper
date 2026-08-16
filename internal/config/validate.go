package config

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var supportedMethods = map[string]struct{}{
	http.MethodDelete:  {},
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodOptions: {},
	http.MethodPatch:   {},
	http.MethodPost:    {},
	http.MethodPut:     {},
}

var reservedPaths = []string{"/health", "/ready", "/metrics"}

// Validate checks whether the configuration is safe to use at runtime.
func (cfg Config) Validate() error {
	if err := validateServer(cfg.Server); err != nil {
		return err
	}
	if err := validateRedis(cfg.Redis); err != nil {
		return err
	}
	if err := validateBackends(cfg.Backends); err != nil {
		return err
	}
	if err := validateRoutes(cfg.Routes, cfg.Backends); err != nil {
		return err
	}

	return nil
}

func validateServer(server ServerConfig) error {
	if server.Address == "" {
		return fmt.Errorf("server.address must not be empty")
	}
	if server.ReadHeaderTimeout.Duration <= 0 {
		return fmt.Errorf("server.read_header_timeout must be greater than zero")
	}
	if server.ShutdownTimeout.Duration <= 0 {
		return fmt.Errorf("server.shutdown_timeout must be greater than zero")
	}
	return nil
}

func validateRedis(redis RedisConfig) error {
	if redis.Address == "" {
		return fmt.Errorf("redis.address must not be empty")
	}
	if redis.Database < 0 {
		return fmt.Errorf("redis.database must not be negative")
	}
	if redis.OperationTimeout.Duration <= 0 {
		return fmt.Errorf("redis.operation_timeout must be greater than zero")
	}
	if redis.FailurePolicy != FailurePolicyOpen && redis.FailurePolicy != FailurePolicyClosed {
		return fmt.Errorf("redis.failure_policy must be %q or %q", FailurePolicyOpen, FailurePolicyClosed)
	}
	return nil
}

func validateBackends(backends map[string]BackendConfig) error {
	if len(backends) == 0 {
		return fmt.Errorf("backends must contain at least one entry")
	}

	for name, backend := range backends {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("backends contains an empty name")
		}
		if err := validateBackendURL(backend.URL); err != nil {
			return fmt.Errorf("backends[%q].url: %w", name, err)
		}
	}
	return nil
}

func validateBackendURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("must be a valid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("must use http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

func validateRoutes(routes []RouteConfig, backends map[string]BackendConfig) error {
	if len(routes) == 0 {
		return fmt.Errorf("routes must contain at least one entry")
	}

	names := make(map[string]struct{}, len(routes))
	matchers := make(map[string]struct{}, len(routes))

	for index, route := range routes {
		field := fmt.Sprintf("routes[%d]", index)

		if route.Name == "" {
			return fmt.Errorf("%s.name must not be empty", field)
		}
		if _, exists := names[route.Name]; exists {
			return fmt.Errorf("%s.name %q is duplicated", field, route.Name)
		}
		names[route.Name] = struct{}{}

		if _, supported := supportedMethods[route.Method]; !supported {
			return fmt.Errorf("%s.method %q is unsupported", field, route.Method)
		}
		if !strings.HasPrefix(route.PathPrefix, "/") {
			return fmt.Errorf("%s.path_prefix must begin with /", field)
		}
		if isReservedPath(route.PathPrefix) {
			return fmt.Errorf("%s.path_prefix %q is reserved", field, route.PathPrefix)
		}

		matcher := route.Method + " " + route.PathPrefix
		if _, exists := matchers[matcher]; exists {
			return fmt.Errorf("%s duplicates route matcher %q", field, matcher)
		}
		matchers[matcher] = struct{}{}

		if _, exists := backends[route.Backend]; !exists {
			return fmt.Errorf("%s.backend %q does not exist", field, route.Backend)
		}
		if err := validateLimit(route.Limit); err != nil {
			return fmt.Errorf("%s.limit: %w", field, err)
		}

		for client, limit := range route.ClientOverrides {
			if strings.TrimSpace(client) == "" {
				return fmt.Errorf("%s.client_overrides contains an empty client key", field)
			}
			if err := validateLimit(limit); err != nil {
				return fmt.Errorf("%s.client_overrides[%q]: %w", field, client, err)
			}
		}
	}

	return nil
}

func validateLimit(limit LimitConfig) error {
	if limit.Capacity <= 0 {
		return fmt.Errorf("capacity must be greater than zero")
	}
	if limit.Refill.Tokens <= 0 {
		return fmt.Errorf("refill.tokens must be greater than zero")
	}
	if limit.Refill.Interval.Duration <= 0 {
		return fmt.Errorf("refill.interval must be greater than zero")
	}
	return nil
}

func isReservedPath(path string) bool {
	for _, reserved := range reservedPaths {
		if path == reserved || strings.HasPrefix(path, reserved+"/") {
			return true
		}
	}
	return false
}
