package limiter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
)

type fakeScriptRunner struct {
	result interface{}
	err    error
	keys   []string
	args   []interface{}
}

type fakePinger struct {
	err         error
	hadDeadline bool
}

func (f *fakePinger) Ping(ctx context.Context) error {
	_, f.hadDeadline = ctx.Deadline()
	return f.err
}

func (f *fakeScriptRunner) Run(_ context.Context, keys []string, args ...interface{}) (interface{}, error) {
	f.keys = keys
	f.args = args
	return f.result, f.err
}

func TestRedisLimiterCheck(t *testing.T) {
	runner := &fakeScriptRunner{result: []interface{}{int64(1), int64(4), int64(0)}}
	limiter := &RedisLimiter{runner: runner, timeout: time.Second, keyPrefix: "gatekeeper:bucket:"}

	decision, err := limiter.Check(context.Background(), validCheckRequest())
	if err != nil {
		t.Fatalf("Check returned an error: %v", err)
	}

	wantDecision := Decision{Allowed: true, Remaining: 4}
	if decision != wantDecision {
		t.Fatalf("decision = %#v, want %#v", decision, wantDecision)
	}
	if !reflect.DeepEqual(runner.keys, []string{"gatekeeper:bucket:search:api_key:abc123"}) {
		t.Fatalf("keys = %#v", runner.keys)
	}
	wantArgs := []interface{}{int64(5), int64(2), int64(60_000_000)}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestRedisLimiterCheckRejected(t *testing.T) {
	runner := &fakeScriptRunner{result: []interface{}{int64(0), int64(0), int64(750)}}
	limiter := &RedisLimiter{runner: runner, timeout: time.Second, keyPrefix: "gatekeeper:bucket:"}

	decision, err := limiter.Check(context.Background(), validCheckRequest())
	if err != nil {
		t.Fatalf("Check returned an error: %v", err)
	}
	if decision.Allowed || decision.Remaining != 0 || decision.RetryAfter != 750*time.Millisecond {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRedisLimiterCheckWrapsRunnerError(t *testing.T) {
	runner := &fakeScriptRunner{err: errors.New("connection refused")}
	limiter := &RedisLimiter{runner: runner, timeout: time.Second, keyPrefix: "gatekeeper:bucket:"}

	_, err := limiter.Check(context.Background(), validCheckRequest())
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error = %v", err)
	}
}

func TestRedisLimiterCheckRejectsMalformedResult(t *testing.T) {
	runner := &fakeScriptRunner{result: []interface{}{int64(1)}}
	limiter := &RedisLimiter{runner: runner, timeout: time.Second, keyPrefix: "gatekeeper:bucket:"}

	_, err := limiter.Check(context.Background(), validCheckRequest())
	if err == nil || !strings.Contains(err.Error(), "expected three values") {
		t.Fatalf("error = %v", err)
	}
}

func TestRedisLimiterCheckValidatesInput(t *testing.T) {
	request := validCheckRequest()
	request.ClientID = ""
	limiter := &RedisLimiter{runner: &fakeScriptRunner{}, timeout: time.Second}

	if _, err := limiter.Check(context.Background(), request); err == nil {
		t.Fatal("Check accepted an empty client ID")
	}
}

func TestRedisLimiterPing(t *testing.T) {
	pinger := &fakePinger{}
	limiter := &RedisLimiter{pinger: pinger, timeout: time.Second}

	if err := limiter.Ping(context.Background()); err != nil {
		t.Fatalf("Ping returned an error: %v", err)
	}
	if !pinger.hadDeadline {
		t.Error("Ping context did not have an operation deadline")
	}
}

func TestRedisLimiterPingWrapsError(t *testing.T) {
	limiter := &RedisLimiter{
		pinger:  &fakePinger{err: errors.New("connection refused")},
		timeout: time.Second,
	}

	err := limiter.Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ping Redis: connection refused") {
		t.Fatalf("error = %v", err)
	}
}

func validCheckRequest() CheckRequest {
	return CheckRequest{
		Route:    "search",
		ClientID: "api_key:abc123",
		Limit: config.LimitConfig{
			Capacity: 5,
			Refill: config.RefillConfig{
				Tokens:   2,
				Interval: config.NewDuration(time.Minute),
			},
		},
	}
}
