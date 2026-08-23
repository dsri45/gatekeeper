# Gatekeeper

Gatekeeper is a distributed rate-limiting API gateway written in Go. It will
use Redis-backed token buckets to enforce per-client and per-route limits before
proxying allowed HTTP requests to backend services.

## Status

The functional HTTP gateway and mock backend are implemented. Distributed Redis
rate limiting is the next milestone.

## Documentation

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Token-bucket specification](docs/rate-limiting.md)

## Requirements

- Go 1.26 or newer
- Docker and Docker Compose (required once Redis and the demo stack are added)

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

The mock backend listens on `http://localhost:8081`. Its application endpoints
are `GET /api/search` and `POST /api/upload`. Test counters are available at
`GET /_mock/stats` and can be cleared with `POST /_mock/reset`.
