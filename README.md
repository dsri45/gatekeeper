# Gatekeeper

Gatekeeper is a distributed rate-limiting API gateway written in Go. It uses
Redis-backed token buckets to enforce per-client and per-route limits before
proxying allowed HTTP requests to backend services.

## Status

The gateway now enforces atomic Redis-backed token buckets, supports per-client
overrides, returns `429 Too Many Requests`, and applies a configurable Redis
failure policy. Prometheus request, latency, and limiter-error metrics are
available at `GET /metrics`. Each request also produces a structured JSON
completion log without client identifiers or query data.

## Documentation

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Token-bucket specification](docs/rate-limiting.md)

## Requirements

- Go 1.26 or newer
- A reachable Redis server for rate-limit enforcement
- Docker Desktop with Docker Compose for the complete demo stack and load tests

## Run locally

Start the mock backend in one terminal:

```powershell
go run ./cmd/mock-backend
```

Start Gatekeeper in a second terminal:

```powershell
go run ./cmd/gateway -config config/config.example.yaml
```

Send a request through Gatekeeper:

```powershell
curl.exe -H "X-API-Key: demo-client" "http://localhost:8080/api/search?q=redis"
```

Inspect Prometheus metrics:

```powershell
curl.exe "http://localhost:8080/metrics"
```

Check liveness or Redis-aware readiness:

```powershell
curl.exe "http://localhost:8080/health"
curl.exe "http://localhost:8080/ready"
```

When Redis is unavailable, readiness reports `degraded` with HTTP 200 under
`fail_open`, or `not_ready` with HTTP 503 under `fail_closed`.

## Build the gateway image

Build the multi-stage, non-root gateway container image:

```powershell
docker build -t gatekeeper:local .
```

Run the standalone image to inspect its health endpoint:

```powershell
docker run --rm --name gatekeeper -p 8080:8080 gatekeeper:local
```

The full Redis and mock-backend network is added through Docker Compose rather
than the standalone container command.

Build the mock-backend image from its separate Dockerfile:

```powershell
docker build -f Dockerfile.mock-backend -t gatekeeper-mock-backend:local .
```

Run it on port `8081`:

```powershell
docker run --rm --name gatekeeper-mock-backend -p 8081:8081 gatekeeper-mock-backend:local
```

## Run the complete stack

Build and start Gatekeeper, Redis, and the mock backend:

```powershell
docker compose up --build --detach
```

Inspect service health:

```powershell
docker compose ps
curl.exe "http://localhost:8080/ready"
```

Stop and remove the Compose containers and private network:

```powershell
docker compose down
```

## Run the full-stack integration test

With the Compose stack running, enable the opt-in Go integration test:

```powershell
$env:GATEKEEPER_INTEGRATION = "1"
go test -v ./tests/integration
Remove-Item Env:GATEKEEPER_INTEGRATION
```

The test gives a unique client five upload tokens and sends six requests. It
requires five `202 Accepted` responses followed by one `429 Too Many Requests`,
then confirms that the mock backend received exactly five requests. It also
checks rate-limit headers, Redis-aware readiness, and Prometheus metrics.

To demonstrate the configured fail-open policy with the stack still running:

```powershell
docker compose stop redis
curl.exe "http://localhost:8080/ready"
curl.exe -H "X-API-Key: fail-open-demo" "http://localhost:8080/api/search?q=redis-down"
docker compose logs gateway
docker compose up --detach --wait redis
```

Readiness reports `degraded`, while the application request still reaches the
backend. The gateway log marks the request as `fail_open` with a Redis error.

## Run the load tests

Start the stack with the deterministic load-test configuration:

```powershell
docker compose -f docker-compose.yml -f docker-compose.loadtest.yml up --build --detach --wait
```

Prove that a 100-token bucket allows exactly 100 of 1000 concurrent requests:

```powershell
docker compose -f docker-compose.yml -f docker-compose.loadtest.yml run --rm k6 run /scripts/exact-limit.js
```

Measure the complete allowed-request path with 20 virtual users for 30 seconds:

```powershell
docker compose -f docker-compose.yml -f docker-compose.loadtest.yml run --rm k6 run /scripts/throughput.js
```

The throughput workload defaults to the stable local benchmark described below.
It can be adjusted with `-e VUS=50` or
`-e DURATION=60s` before the `k6` service name. Stop the benchmark stack using
the same two Compose files:

```powershell
docker compose -f docker-compose.yml -f docker-compose.loadtest.yml down
```

## Load-test results

These results were measured locally on August 23, 2026 using Docker Desktop
4.87.0, Docker Engine 29.7.2, and k6 2.2.0. Gateway request logging remained
enabled. Each 30-second throughput trial used 20 concurrent virtual users and a
fresh Compose stack followed by a five-second warm-up. The table reports the
complete path through Gatekeeper, Redis, and the mock backend.

| Trial | Allowed requests | Throughput | p50 | p95 | p99 | Unexpected responses |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 164,383 | 5,478 req/s | 3.39 ms | 5.77 ms | 7.36 ms | 0 |
| 2 | 166,302 | 5,542 req/s | 3.35 ms | 5.72 ms | 7.16 ms | 0 |
| 3 | 167,238 | 5,573 req/s | 3.34 ms | 5.68 ms | 7.17 ms | 0 |
| **Median** | **166,302** | **5,542 req/s** | **3.35 ms** | **5.72 ms** | **7.17 ms** | **0** |

The backend counter equaled the number of allowed requests in every trial,
showing that successful gateway responses represented completed proxy traffic.
These are local development-machine results, not production capacity claims.

The separate concurrency test sent 1,000 requests at once to a fresh bucket
containing 100 tokens. It allowed exactly 100 requests, rejected exactly 900
with HTTP 429, produced no unexpected responses, and recorded exactly 100
requests at the backend. This demonstrates that the Redis Lua script performs
the token check and update atomically under concurrent access.

Saved machine-readable k6 summaries are available in
[`loadtest/results`](loadtest/results).

## Resume bullet

> Built a distributed rate-limiting API gateway in Go with Redis-backed atomic
> token buckets, sustaining 5,542 requests/second at 7.17 ms p99 latency in a
> three-run local Docker benchmark with zero unexpected responses.

The mock backend listens on `http://localhost:8081`. Its application endpoints
are `GET /api/search` and `POST /api/upload`. Test counters are available at
`GET /_mock/stats` and can be cleared with `POST /_mock/reset`.
