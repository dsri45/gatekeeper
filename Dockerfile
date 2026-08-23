# syntax=docker/dockerfile:1

FROM golang:1.26.6-alpine3.24 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/gatekeeper \
    ./cmd/gateway

FROM alpine:3.24.1 AS runtime

RUN apk add --no-cache ca-certificates \
    && addgroup -S gatekeeper \
    && adduser -S -D -H -u 10001 -G gatekeeper gatekeeper

WORKDIR /app

COPY --from=build --chown=gatekeeper:gatekeeper /out/gatekeeper /usr/local/bin/gatekeeper
COPY --chown=gatekeeper:gatekeeper config/config.example.yaml /app/config/config.example.yaml

USER gatekeeper

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=2s --start-period=5s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:8080/health > /dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/gatekeeper"]
CMD ["-config", "/app/config/config.example.yaml"]
