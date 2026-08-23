package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadExample(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "config", "config.example.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) returned an error: %v", path, err)
	}

	if cfg.Server.Address != ":8080" {
		t.Errorf("Server.Address = %q, want %q", cfg.Server.Address, ":8080")
	}
	if cfg.Redis.FailurePolicy != FailurePolicyOpen {
		t.Errorf("Redis.FailurePolicy = %q, want %q", cfg.Redis.FailurePolicy, FailurePolicyOpen)
	}
	if len(cfg.Backends) != 1 {
		t.Errorf("len(Backends) = %d, want 1", len(cfg.Backends))
	}
	if len(cfg.Routes) != 2 {
		t.Fatalf("len(Routes) = %d, want 2", len(cfg.Routes))
	}
	if cfg.Routes[0].Limit.Capacity != 50 {
		t.Errorf("search capacity = %d, want 50", cfg.Routes[0].Limit.Capacity)
	}
	if cfg.Routes[0].ClientOverrides["demo-premium-client"].Capacity != 100 {
		t.Errorf("premium client capacity = %d, want 100", cfg.Routes[0].ClientOverrides["demo-premium-client"].Capacity)
	}
}

func TestDecodeAppliesDefaultsAndNormalizesMethod(t *testing.T) {
	t.Parallel()

	cfg, err := decode(strings.NewReader(minimalYAML("get")))
	if err != nil {
		t.Fatalf("decode returned an error: %v", err)
	}

	if cfg.Server.Address != defaultServerAddress {
		t.Errorf("Server.Address = %q, want %q", cfg.Server.Address, defaultServerAddress)
	}
	if cfg.Server.ReadHeaderTimeout.Duration != defaultReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %s, want %s", cfg.Server.ReadHeaderTimeout.Duration, defaultReadHeaderTimeout)
	}
	if cfg.Server.ReadTimeout.Duration != defaultReadTimeout {
		t.Errorf("ReadTimeout = %s, want %s", cfg.Server.ReadTimeout.Duration, defaultReadTimeout)
	}
	if cfg.Server.WriteTimeout.Duration != defaultWriteTimeout {
		t.Errorf("WriteTimeout = %s, want %s", cfg.Server.WriteTimeout.Duration, defaultWriteTimeout)
	}
	if cfg.Server.IdleTimeout.Duration != defaultIdleTimeout {
		t.Errorf("IdleTimeout = %s, want %s", cfg.Server.IdleTimeout.Duration, defaultIdleTimeout)
	}
	if cfg.Server.ShutdownTimeout.Duration != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s, want %s", cfg.Server.ShutdownTimeout.Duration, defaultShutdownTimeout)
	}
	if cfg.Redis.Database != defaultRedisDatabase {
		t.Errorf("Redis.Database = %d, want %d", cfg.Redis.Database, defaultRedisDatabase)
	}
	if cfg.Redis.OperationTimeout.Duration != defaultRedisTimeout {
		t.Errorf("Redis.OperationTimeout = %s, want %s", cfg.Redis.OperationTimeout.Duration, defaultRedisTimeout)
	}
	if cfg.Redis.FailurePolicy != defaultRedisFailurePolicy {
		t.Errorf("Redis.FailurePolicy = %q, want %q", cfg.Redis.FailurePolicy, defaultRedisFailurePolicy)
	}
	if cfg.Routes[0].Method != "GET" {
		t.Errorf("route method = %q, want GET", cfg.Routes[0].Method)
	}
}

func TestDecodeRejectsInvalidYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "malformed YAML",
			yaml:    "redis: [",
			wantErr: "decode YAML",
		},
		{
			name: "unknown field",
			yaml: minimalYAML("GET") + `
unexpected_setting: true
`,
			wantErr: "field unexpected_setting not found",
		},
		{
			name: "invalid duration",
			yaml: `
server:
  read_header_timeout: "later"
` + minimalYAML("GET"),
			wantErr: "invalid duration",
		},
		{
			name: "multiple documents",
			yaml: minimalYAML("GET") + `
---
redis:
  address: "another:6379"
`,
			wantErr: "multiple YAML documents",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := decode(strings.NewReader(test.yaml))
			assertErrorContains(t, err, test.wantErr)
		})
	}
}

func TestLoadRejectsMissingFile(t *testing.T) {
	t.Parallel()

	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	assertErrorContains(t, err, "open config")
}

func TestValidateRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "empty server address",
			mutate: func(cfg *Config) {
				cfg.Server.Address = ""
			},
			wantErr: "server.address",
		},
		{
			name: "non-positive header read timeout",
			mutate: func(cfg *Config) {
				cfg.Server.ReadHeaderTimeout = NewDuration(0)
			},
			wantErr: "server.read_header_timeout",
		},
		{
			name: "non-positive request read timeout",
			mutate: func(cfg *Config) {
				cfg.Server.ReadTimeout = NewDuration(0)
			},
			wantErr: "server.read_timeout",
		},
		{
			name: "non-positive write timeout",
			mutate: func(cfg *Config) {
				cfg.Server.WriteTimeout = NewDuration(0)
			},
			wantErr: "server.write_timeout",
		},
		{
			name: "non-positive idle timeout",
			mutate: func(cfg *Config) {
				cfg.Server.IdleTimeout = NewDuration(0)
			},
			wantErr: "server.idle_timeout",
		},
		{
			name: "non-positive shutdown timeout",
			mutate: func(cfg *Config) {
				cfg.Server.ShutdownTimeout = NewDuration(-time.Second)
			},
			wantErr: "server.shutdown_timeout",
		},
		{
			name: "empty Redis address",
			mutate: func(cfg *Config) {
				cfg.Redis.Address = ""
			},
			wantErr: "redis.address",
		},
		{
			name: "negative Redis database",
			mutate: func(cfg *Config) {
				cfg.Redis.Database = -1
			},
			wantErr: "redis.database",
		},
		{
			name: "non-positive Redis timeout",
			mutate: func(cfg *Config) {
				cfg.Redis.OperationTimeout = NewDuration(0)
			},
			wantErr: "redis.operation_timeout",
		},
		{
			name: "unsupported Redis failure policy",
			mutate: func(cfg *Config) {
				cfg.Redis.FailurePolicy = "sometimes"
			},
			wantErr: "redis.failure_policy",
		},
		{
			name: "no backends",
			mutate: func(cfg *Config) {
				cfg.Backends = nil
			},
			wantErr: "backends must contain",
		},
		{
			name: "empty backend name",
			mutate: func(cfg *Config) {
				cfg.Backends[""] = BackendConfig{URL: "http://backend:8080"}
			},
			wantErr: "empty name",
		},
		{
			name: "invalid backend scheme",
			mutate: func(cfg *Config) {
				cfg.Backends["mock"] = BackendConfig{URL: "ftp://backend/file"}
			},
			wantErr: "must use http or https",
		},
		{
			name: "backend URL without host",
			mutate: func(cfg *Config) {
				cfg.Backends["mock"] = BackendConfig{URL: "http:///missing-host"}
			},
			wantErr: "must include a host",
		},
		{
			name: "no routes",
			mutate: func(cfg *Config) {
				cfg.Routes = nil
			},
			wantErr: "routes must contain",
		},
		{
			name: "empty route name",
			mutate: func(cfg *Config) {
				cfg.Routes[0].Name = ""
			},
			wantErr: "routes[0].name",
		},
		{
			name: "duplicate route name",
			mutate: func(cfg *Config) {
				second := cfg.Routes[0]
				second.PathPrefix = "/api/second"
				cfg.Routes = append(cfg.Routes, second)
			},
			wantErr: "name \"search\" is duplicated",
		},
		{
			name: "unsupported method",
			mutate: func(cfg *Config) {
				cfg.Routes[0].Method = "CONNECT"
			},
			wantErr: "method \"CONNECT\" is unsupported",
		},
		{
			name: "path without leading slash",
			mutate: func(cfg *Config) {
				cfg.Routes[0].PathPrefix = "api/search"
			},
			wantErr: "path_prefix must begin with /",
		},
		{
			name: "reserved path",
			mutate: func(cfg *Config) {
				cfg.Routes[0].PathPrefix = "/metrics/details"
			},
			wantErr: "is reserved",
		},
		{
			name: "duplicate route matcher",
			mutate: func(cfg *Config) {
				second := cfg.Routes[0]
				second.Name = "search-copy"
				cfg.Routes = append(cfg.Routes, second)
			},
			wantErr: "duplicates route matcher",
		},
		{
			name: "unknown backend",
			mutate: func(cfg *Config) {
				cfg.Routes[0].Backend = "missing"
			},
			wantErr: "backend \"missing\" does not exist",
		},
		{
			name: "invalid capacity",
			mutate: func(cfg *Config) {
				cfg.Routes[0].Limit.Capacity = 0
			},
			wantErr: "limit: capacity",
		},
		{
			name: "invalid refill tokens",
			mutate: func(cfg *Config) {
				cfg.Routes[0].Limit.Refill.Tokens = -1
			},
			wantErr: "refill.tokens",
		},
		{
			name: "invalid refill interval",
			mutate: func(cfg *Config) {
				cfg.Routes[0].Limit.Refill.Interval = NewDuration(0)
			},
			wantErr: "refill.interval",
		},
		{
			name: "empty client override key",
			mutate: func(cfg *Config) {
				cfg.Routes[0].ClientOverrides[""] = validLimit()
			},
			wantErr: "empty client key",
		},
		{
			name: "invalid client override",
			mutate: func(cfg *Config) {
				limit := validLimit()
				limit.Capacity = 0
				cfg.Routes[0].ClientOverrides["client"] = limit
			},
			wantErr: "client_overrides[\"client\"]: capacity",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := validConfig()
			test.mutate(&cfg)
			assertErrorContains(t, cfg.Validate(), test.wantErr)
		})
	}
}

func minimalYAML(method string) string {
	return `
redis:
  address: "redis:6379"
backends:
  mock:
    url: "http://mock-backend:8081"
routes:
  - name: "search"
    method: "` + method + `"
    path_prefix: "/api/search"
    backend: "mock"
    limit:
      capacity: 10
      refill:
        tokens: 10
        interval: "1m"
`
}

func validConfig() Config {
	cfg := defaults()
	cfg.Redis.Address = "redis:6379"
	cfg.Backends = map[string]BackendConfig{
		"mock": {URL: "http://mock-backend:8081"},
	}
	cfg.Routes = []RouteConfig{
		{
			Name:            "search",
			Method:          "GET",
			PathPrefix:      "/api/search",
			Backend:         "mock",
			Limit:           validLimit(),
			ClientOverrides: make(map[string]LimitConfig),
		},
	}
	return cfg
}

func validLimit() LimitConfig {
	return LimitConfig{
		Capacity: 10,
		Refill: RefillConfig{
			Tokens:   10,
			Interval: NewDuration(time.Minute),
		},
	}
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}
