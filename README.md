# Gatekeeper

Gatekeeper is a distributed rate-limiting API gateway written in Go. It will
use Redis-backed token buckets to enforce per-client and per-route limits before
proxying allowed HTTP requests to backend services.

## Status

Initial project scaffold. Implementation is in progress.

## Documentation

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)

## Requirements

- Go 1.26 or newer
- Docker and Docker Compose (required once Redis and the demo stack are added)

## Run the current scaffold

```powershell
go run ./cmd/gateway
```

## Run the mock backend

```powershell
go run ./cmd/mock-backend
```

The mock backend listens on `http://localhost:8081`. Its application endpoints
are `GET /api/search` and `POST /api/upload`. Test counters are available at
`GET /_mock/stats` and can be cleared with `POST /_mock/reset`.
