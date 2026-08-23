# Token-Bucket Specification

## Purpose

Gatekeeper uses a Redis-backed token bucket for each client-and-route pair. The
algorithm permits a bounded burst while enforcing the configured average refill
rate. Every bucket transition must remain correct when requests reach different
gateway instances concurrently.

This document defines the behavior that the Redis Lua script and Go limiter
must implement.

## Configuration inputs

Each route rule provides:

```yaml
limit:
  capacity: 50
  refill:
    tokens: 50
    interval: "1m"
```

The values mean:

```text
capacity       Maximum token balance
refill.tokens  Tokens restored during one refill interval
interval       Duration used to calculate the continuous refill rate
```

Every request costs exactly one token.

## Bucket identity

Each client receives an independent bucket for each matched route. Redis keys
use this structure:

```text
gatekeeper:bucket:<route-name>:<hashed-client-identity>
```

The client identity already includes its namespace:

```text
api-key:<SHA-256 digest>
ip:<SHA-256 digest>
```

Raw API keys must never appear in Redis keys.

## Stored state

One Redis hash stores two fields:

```text
tokens                  Fractional token balance
last_refill_us          Last update time in Unix microseconds
```

The token balance is stored as a decimal value so partial refill progress is
not discarded.

## Time source

The Lua script obtains the current time from Redis using `TIME`. It converts the
returned seconds and microseconds into one Unix-microsecond value.

Using Redis time gives every gateway instance the same clock for a shared
bucket. If the calculated elapsed time is negative, the script clamps it to
zero. A backward clock adjustment must not remove tokens or create a negative
refill.

## New buckets

When neither state field exists, the script initializes the bucket with:

```text
tokens         = capacity
last_refill_us = current Redis time
```

The current request is then evaluated against that full balance. A bucket with
capacity 5 therefore permits an immediate burst of 5 requests.

Partially present or nonnumeric state is invalid. The script must return an
error rather than silently resetting a corrupted bucket to full capacity.

## Continuous refill

The refill rate is:

```text
refill rate = refill tokens / interval microseconds
```

For existing state:

```text
elapsed_us = max(0, now_us - last_refill_us)

refilled = elapsed_us × refill_tokens / interval_us

available = min(capacity, stored_tokens + refilled)
```

Fractional values remain in `available`. A rule that restores 5 tokens per
minute therefore makes progress continuously rather than adding tokens only at
the end of each minute.

## Decision and consumption

The decision is made after refill:

```text
if available >= 1:
    allowed = true
    available = available - 1
else:
    allowed = false
```

A rejected request never consumes a token.

After the decision, the script stores `available` and the current Redis time.
The full transition occurs in one Lua execution.

## Atomicity requirement

The script performs the complete transition without another Redis command
interleaving with it:

```text
read → refill → cap → decide → consume if allowed → store → expire
```

If one token remains and two requests arrive concurrently, Redis processes one
script before the other. The first request consumes the token. The second sees
the updated zero-token balance and is rejected.

No authoritative token balance is stored in gateway process memory.

## Returned values

The script returns three integer values:

```text
allowed         1 when allowed, 0 when rejected
remaining       Immediately usable whole tokens after the decision
retry_after_ms  Milliseconds until one token is available
```

`remaining` uses:

```text
remaining = floor(available)
```

An internal balance of `4.8` therefore reports `4` remaining requests.

## Retry calculation

Allowed requests return a retry delay of zero.

For a rejected request:

```text
missing_tokens = 1 - available

retry_us = ceil(
    missing_tokens × interval_us / refill_tokens
)

retry_after_ms = max(1, ceil(retry_us / 1000))
```

Gatekeeper converts milliseconds to the HTTP `Retry-After` header by rounding
up to a whole second:

```text
retry_after_seconds = max(1, ceil(retry_after_ms / 1000))
```

Rounding upward prevents a client from retrying before a complete token exists.

## Bucket expiration

Every successful script execution refreshes the Redis key's TTL. The TTL is the
time an empty bucket requires to refill to capacity:

```text
ttl_ms = max(
    1,
    ceil(capacity × interval_ms / refill_tokens)
)
```

After this idle period, the bucket is full. Deleting it is semantically
equivalent to retaining a full bucket, while preventing inactive client state
from accumulating indefinitely.

## Lua arguments

The script will receive one key and these numeric arguments:

```text
KEYS[1]  Bucket Redis key

ARGV[1]  Capacity
ARGV[2]  Refill tokens
ARGV[3]  Refill interval in microseconds
```

The script obtains time internally. Gateway clocks are not passed as arguments.

All arguments must be positive. The Go configuration loader validates them
before startup, and the script will still reject invalid values defensively.

## Error behavior

The script returns a Redis error when:

- an argument is missing or nonnumeric;
- capacity is not positive;
- refill tokens are not positive;
- the refill interval is not positive; or
- stored bucket state is incomplete or nonnumeric.

The Go limiter will treat script errors as Redis dependency failures. The
configured fail-open or fail-closed policy is applied by the gateway layer in a
later step.

## Correctness tests

### Initial burst

For capacity 5, a fresh bucket allows the first 5 immediate requests. The next
request is rejected when no refill has completed.

### Capacity cap

An idle bucket never exceeds capacity, even when elapsed time would calculate a
larger refill.

### Fractional progress

Rejected requests preserve partial tokens. Repeated checks do not reset refill
progress.

### Retry delay

The returned delay is the earliest rounded-up duration at which one full token
will exist.

### Concurrent burst

The atomicity test uses:

```text
capacity: 100
requests: 1,000 concurrent
refill: slow enough that no full token returns during the burst
```

The expected result is exactly:

```text
100 allowed
900 rejected
```

Refill behavior is verified separately with controlled elapsed time and
sustained traffic. This prevents legitimate refill during a long burst from
being mistaken for an atomicity failure.

## Algorithm choice

Token bucket is preferred over a fixed-window counter because fixed windows can
produce a large boundary spike. A sliding-window log provides accurate rolling
limits but stores more per-request data. A leaky bucket is better suited to
queueing work at a steady output rate, while Gatekeeper requires an immediate
allow-or-reject decision.

The token bucket keeps only two state fields per client-and-route pair. It
supports controlled bursts and maintains a continuous average rate.

