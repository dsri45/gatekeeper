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
- Docker and Docker Compose once the containerized demo stack is added

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

The mock backend listens on `http://localhost:8081`. Its application endpoints
are `GET /api/search` and `POST /api/upload`. Test counters are available at
`GET /_mock/stats` and can be cleared with `POST /_mock/reset`.
