package limiter

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
)

func TestRedisLimiterConcurrentExactLimit(t *testing.T) {
	address := os.Getenv("REDIS_ADDR")
	if address == "" {
		t.Skip("set REDIS_ADDR to run the Redis integration test")
	}

	redisLimiter, err := NewRedis(config.RedisConfig{
		Address:          address,
		OperationTimeout: config.NewDuration(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	t.Cleanup(func() { _ = redisLimiter.Close() })

	request := CheckRequest{
		Route:    "integration-test",
		ClientID: fmt.Sprintf("api_key:%d", time.Now().UnixNano()),
		Limit: config.LimitConfig{
			Capacity: 25,
			Refill: config.RefillConfig{
				Tokens:   1,
				Interval: config.NewDuration(time.Hour),
			},
		},
	}

	var allowed atomic.Int64
	var waitGroup sync.WaitGroup
	for range 100 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			decision, checkErr := redisLimiter.Check(context.Background(), request)
			if checkErr != nil {
				t.Errorf("Check: %v", checkErr)
				return
			}
			if decision.Allowed {
				allowed.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	if got := allowed.Load(); got != 25 {
		t.Fatalf("allowed = %d, want exactly 25", got)
	}
}
