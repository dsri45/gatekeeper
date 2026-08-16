# Gatekeeper Configuration

Gatekeeper reads its startup settings from a YAML file. The configuration
defines the HTTP server and Redis connection. It also maps application routes
to backends and rate-limit rules.

See [`config/config.example.yaml`](../config/config.example.yaml) for a complete
example.

## Server

```yaml
server:
  address: ":8080"
  read_header_timeout: "5s"
  shutdown_timeout: "10s"
```

| Field | Required | Default | Meaning |
| --- | --- | --- | --- |
| `address` | No | `:8080` | Network address used by the HTTP server |
| `read_header_timeout` | No | `5s` | Maximum time allowed for reading request headers |
| `shutdown_timeout` | No | `10s` | Maximum time allowed for active requests to finish during shutdown |

Duration values use Go duration syntax. Common units include `ms` for
milliseconds, `s` for seconds, and `m` for minutes.

## Redis

```yaml
redis:
  address: "redis:6379"
  database: 0
  operation_timeout: "100ms"
  failure_policy: "fail_open"
```

| Field | Required | Default | Meaning |
| --- | --- | --- | --- |
| `address` | Yes | None | Redis host and port |
| `database` | No | `0` | Redis logical database number |
| `operation_timeout` | No | `100ms` | Maximum time for one rate-limit operation |
| `failure_policy` | No | `fail_open` | Behavior when Redis cannot make a rate-limit decision |

The supported failure policies are:

- `fail_open`: forward the request and report the Redis failure;
- `fail_closed`: reject the request because its limit cannot be checked.

## Backends

```yaml
backends:
  mock:
    url: "http://mock-backend:8081"
```

`backends` is a map from a unique backend name to its base URL. Routes use the
short name rather than repeating the URL.

Backend names must not be empty. Every URL must be an absolute `http` or
`https` URL with a host.

## Routes

```yaml
routes:
  - name: "search"
    method: "GET"
    path_prefix: "/api/search"
    backend: "mock"
    limit:
      capacity: 50
      refill:
        tokens: 50
        interval: "1m"
```

| Field | Required | Meaning |
| --- | --- | --- |
| `name` | Yes | Stable unique name used in Redis keys, logs, and metrics |
| `method` | Yes | HTTP method matched exactly after normalization to uppercase |
| `path_prefix` | Yes | Beginning of the request path to match |
| `backend` | Yes | Name of an entry in the `backends` map |
| `limit` | Yes | Default token-bucket rule for this route |
| `client_overrides` | No | Replacement rules for named API keys |

If several routes match a request, the route with the longest `path_prefix`
wins. Method matching remains exact. A request with no matching route receives
`404 Not Found`.

The reserved prefixes `/health`, `/ready`, and `/metrics` cannot be configured
as application routes.

## Rate-limit rules

```yaml
limit:
  capacity: 50
  refill:
    tokens: 50
    interval: "1m"
```

`capacity` is the maximum number of tokens in the bucket. A new bucket starts
at this capacity.

`refill.tokens` and `refill.interval` define a continuous refill rate. The
example adds tokens at an average rate of 50 per minute. Tokens never accumulate
beyond `capacity`.

Every allowed request costs one token. Capacity must be positive. Refill tokens
must also be positive, and the interval must be a valid positive duration.

## Client overrides

```yaml
client_overrides:
  demo-premium-client:
    capacity: 100
    refill:
      tokens: 100
      interval: "1m"
```

Each key under `client_overrides` matches an exact `X-API-Key` value. The
override replaces the route's default rule for that client.

The checked-in example uses a fake demonstration key. Real API keys are
secrets and should not be committed to source control. Secret injection is a
deployment concern that can be added without changing the route model.

## Defaults

| Setting | Default |
| --- | --- |
| Server address | `:8080` |
| Header-read timeout | `5s` |
| Shutdown timeout | `10s` |
| Redis database | `0` |
| Redis operation timeout | `100ms` |
| Redis failure policy | `fail_open` |

Backends and routes do not receive defaults. Each route must explicitly name
its method and path prefix. It must also select a backend and provide a limit.

## Validation rules

Gatekeeper will reject the configuration before starting when any of these
conditions is present:

- the Redis address is missing;
- a timeout or duration is invalid;
- the Redis database number is negative;
- the failure policy is unsupported;
- a backend name is empty;
- a backend URL is not a valid absolute HTTP URL;
- a route name is empty or duplicated;
- a route method is missing or unsupported;
- a path prefix does not begin with `/`;
- a route uses a reserved internal prefix;
- two routes have the same method and path prefix;
- a route references an unknown backend;
- a capacity is not positive;
- refill tokens are not positive;
- a refill interval is not positive; or
- a client override contains an invalid limit.

Validation errors will identify the field that failed so configuration problems
can be corrected without debugging the running server.

