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

The mock backend listens on `http://localhost:8081`. Its application endpoints
are `GET /api/search` and `POST /api/upload`. Test counters are available at
`GET /_mock/stats` and can be cleared with `POST /_mock/reset`.
