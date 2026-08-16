package config

import "time"

const (
	FailurePolicyOpen   = "fail_open"
	FailurePolicyClosed = "fail_closed"
)

const (
	defaultServerAddress      = ":8080"
	defaultReadHeaderTimeout  = 5 * time.Second
	defaultShutdownTimeout    = 10 * time.Second
	defaultRedisDatabase      = 0
	defaultRedisTimeout       = 100 * time.Millisecond
	defaultRedisFailurePolicy = FailurePolicyOpen
)

// Config contains all settings required to run Gatekeeper.
type Config struct {
	Server   ServerConfig             `yaml:"server"`
	Redis    RedisConfig              `yaml:"redis"`
	Backends map[string]BackendConfig `yaml:"backends"`
	Routes   []RouteConfig            `yaml:"routes"`
}

// ServerConfig controls the public HTTP server.
type ServerConfig struct {
	Address           string   `yaml:"address"`
	ReadHeaderTimeout Duration `yaml:"read_header_timeout"`
	ShutdownTimeout   Duration `yaml:"shutdown_timeout"`
}

// RedisConfig controls the connection to the shared rate-limit store.
type RedisConfig struct {
	Address          string   `yaml:"address"`
	Database         int      `yaml:"database"`
	OperationTimeout Duration `yaml:"operation_timeout"`
	FailurePolicy    string   `yaml:"failure_policy"`
}

// BackendConfig identifies one upstream HTTP service.
type BackendConfig struct {
	URL string `yaml:"url"`
}

// RouteConfig maps an incoming request to a backend and token bucket.
type RouteConfig struct {
	Name            string                 `yaml:"name"`
	Method          string                 `yaml:"method"`
	PathPrefix      string                 `yaml:"path_prefix"`
	Backend         string                 `yaml:"backend"`
	Limit           LimitConfig            `yaml:"limit"`
	ClientOverrides map[string]LimitConfig `yaml:"client_overrides"`
}

// LimitConfig defines a token bucket's capacity and refill behavior.
type LimitConfig struct {
	Capacity int64        `yaml:"capacity"`
	Refill   RefillConfig `yaml:"refill"`
}

// RefillConfig defines the average rate at which tokens return to a bucket.
type RefillConfig struct {
	Tokens   int64    `yaml:"tokens"`
	Interval Duration `yaml:"interval"`
}

func defaults() Config {
	return Config{
		Server: ServerConfig{
			Address:           defaultServerAddress,
			ReadHeaderTimeout: NewDuration(defaultReadHeaderTimeout),
			ShutdownTimeout:   NewDuration(defaultShutdownTimeout),
		},
		Redis: RedisConfig{
			Database:         defaultRedisDatabase,
			OperationTimeout: NewDuration(defaultRedisTimeout),
			FailurePolicy:    defaultRedisFailurePolicy,
		},
	}
}
