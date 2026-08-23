package limiter

import (
	"context"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
)

// Limiter decides whether one request may consume a token.
type Limiter interface {
	Check(context.Context, CheckRequest) (Decision, error)
}

// CheckRequest identifies the bucket and supplies its configured rate limit.
type CheckRequest struct {
	Route    string
	ClientID string
	Limit    config.LimitConfig
}

// Decision is the result of one atomic token-bucket operation.
type Decision struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
}
