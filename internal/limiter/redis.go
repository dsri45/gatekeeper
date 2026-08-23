package limiter

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
	"github.com/redis/go-redis/v9"
)

//go:embed token_bucket.lua
var tokenBucketScript string

type scriptRunner interface {
	Run(context.Context, []string, ...interface{}) (interface{}, error)
}

type redisScriptRunner struct {
	client *redis.Client
	script *redis.Script
}

func (r redisScriptRunner) Run(ctx context.Context, keys []string, args ...interface{}) (interface{}, error) {
	return r.script.Run(ctx, r.client, keys, args...).Result()
}

// RedisLimiter stores token buckets in Redis and updates them atomically.
type RedisLimiter struct {
	runner    scriptRunner
	client    *redis.Client
	timeout   time.Duration
	keyPrefix string
}

// NewRedis creates a limiter backed by a shared Redis connection pool.
func NewRedis(cfg config.RedisConfig) (*RedisLimiter, error) {
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, errors.New("redis address is required")
	}
	if cfg.Database < 0 {
		return nil, errors.New("redis database must not be negative")
	}
	if cfg.OperationTimeout.Duration <= 0 {
		return nil, errors.New("redis operation timeout must be positive")
	}

	client := redis.NewClient(&redis.Options{
		Addr: cfg.Address,
		DB:   cfg.Database,
	})

	return &RedisLimiter{
		runner:    redisScriptRunner{client: client, script: redis.NewScript(tokenBucketScript)},
		client:    client,
		timeout:   cfg.OperationTimeout.Duration,
		keyPrefix: "gatekeeper:bucket:",
	}, nil
}

// Check atomically refills a bucket and consumes one token when available.
func (l *RedisLimiter) Check(ctx context.Context, request CheckRequest) (Decision, error) {
	if l == nil || l.runner == nil {
		return Decision{}, errors.New("redis limiter is not initialized")
	}
	if err := validateCheckRequest(request); err != nil {
		return Decision{}, err
	}

	operationContext, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	result, err := l.runner.Run(
		operationContext,
		[]string{l.keyPrefix + request.Route + ":" + request.ClientID},
		request.Limit.Capacity,
		request.Limit.Refill.Tokens,
		request.Limit.Refill.Interval.Duration.Microseconds(),
	)
	if err != nil {
		return Decision{}, fmt.Errorf("run Redis token bucket: %w", err)
	}

	decision, err := parseDecision(result)
	if err != nil {
		return Decision{}, fmt.Errorf("parse Redis token-bucket result: %w", err)
	}
	return decision, nil
}

// Close releases the Redis client's connections.
func (l *RedisLimiter) Close() error {
	if l == nil || l.client == nil {
		return nil
	}
	return l.client.Close()
}

func validateCheckRequest(request CheckRequest) error {
	if strings.TrimSpace(request.Route) == "" {
		return errors.New("route must be non-empty")
	}
	if strings.TrimSpace(request.ClientID) == "" {
		return errors.New("client ID must be non-empty")
	}
	if request.Limit.Capacity <= 0 || request.Limit.Refill.Tokens <= 0 {
		return errors.New("capacity and refill tokens must be positive")
	}
	interval := request.Limit.Refill.Interval.Duration
	if interval <= 0 || interval.Microseconds() <= 0 {
		return errors.New("refill interval must be at least one microsecond")
	}
	return nil
}

func parseDecision(result interface{}) (Decision, error) {
	values, ok := result.([]interface{})
	if !ok || len(values) != 3 {
		return Decision{}, fmt.Errorf("expected three values, got %T", result)
	}

	allowed, err := redisInteger(values[0], "allowed")
	if err != nil || (allowed != 0 && allowed != 1) {
		if err != nil {
			return Decision{}, err
		}
		return Decision{}, fmt.Errorf("allowed must be 0 or 1, got %d", allowed)
	}
	remaining, err := redisInteger(values[1], "remaining")
	if err != nil {
		return Decision{}, err
	}
	retryMilliseconds, err := redisInteger(values[2], "retry after")
	if err != nil {
		return Decision{}, err
	}
	if remaining < 0 || retryMilliseconds < 0 {
		return Decision{}, errors.New("remaining and retry after must not be negative")
	}

	return Decision{
		Allowed:    allowed == 1,
		Remaining:  remaining,
		RetryAfter: time.Duration(retryMilliseconds) * time.Millisecond,
	}, nil
}

func redisInteger(value interface{}, name string) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err == nil {
			return parsed, nil
		}
	case []byte:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		if err == nil {
			return parsed, nil
		}
	}
	return 0, fmt.Errorf("%s is not an integer", name)
}
