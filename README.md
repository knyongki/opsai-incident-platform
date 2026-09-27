# OpsAI

OpsAI is an AI-assisted incident-analysis platform for industrial equipment,
written in Go. This repository currently implements **Milestone 1: Go API
Foundation** only. The full roadmap is in [the blueprint](docs/OpsAI-Project-Blueprint.md).
Databases, equipment workflows, workers, AI, MQTT, and authentication are future work.

## Run locally

Prerequisite: Go 1.26.5 or newer, matching the existing `go.mod`. This milestone
uses only the standard library and requires no external services.

From the repository root:

```sh
go run ./cmd/api
```

The server listens on `127.0.0.1:8080`. JSON logs go to standard error. Stop it
with Ctrl+C; SIGTERM also triggers graceful shutdown on systems supporting it.
Shutdown stops accepting connections and allows active requests up to 10 seconds
to finish before closing remaining connections. Startup/shutdown failures exit nonzero.

Configuration comes from process environment variables; `.env.example` is a
safe reference, **not automatically loaded**. Do not create or commit `.env` files.

| Variable | Default | Meaning |
| --- | --- | --- |
| `APP_ENV` | `development` | Environment label; no behavior changes in this milestone |
| `HTTP_ADDRESS` | `127.0.0.1:8080` | Explicit host and numeric port (1–65535) |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, or `ERROR` (case-insensitive) |

Unset or empty values use defaults. Invalid configuration fails at startup.
For example, in PowerShell:

```powershell
$env:HTTP_ADDRESS = '127.0.0.1:9090'
$env:LOG_LEVEL = 'DEBUG'
go run ./cmd/api
```

## Endpoints

In a second terminal, using the default address (use `curl.exe` in Windows PowerShell):

```sh
curl -i http://127.0.0.1:8080/health
curl -i http://127.0.0.1:8080/ready
```

Both return HTTP 200 with `Content-Type: application/json`:

```json
{"data":{"status":"ok"}}
```

`/ready` returns `{"data":{"status":"ready"}}`. Health reports process liveness;
readiness currently means the API can serve requests. There are no database or
external dependency checks yet. Both routes support HEAD with no response body.

Unknown paths return HTTP 404:

```json
{"error":{"code":"NOT_FOUND","message":"The requested resource was not found."}}
```

Unsupported methods on known paths return HTTP 405 with the same error envelope
and `Allow: GET, HEAD`. Paths match exactly; `/health/` is not `/health`.
Application responses use JSON; malformed HTTP rejected by Go before routing
uses the standard server's protocol-level responses.

## Structure and Go concepts

- `cmd/api`: executable entry point and process lifecycle. A `main` package builds a program.
- `internal/config`: validated configuration, represented by a Go struct.
- `internal/platform/logging`: injected `slog.Logger` with structured JSON output.
- `internal/transport/http`: HTTP handlers, response envelopes, and server construction.

Go restricts imports of `internal` packages to this module's directory tree.
Handlers implement the standard `http.Handler` interface, making them easy to
exercise without starting a server using `httptest`. A goroutine runs the HTTP
server while the main flow waits for a signal; a channel carries the server's
result. `context.Context` conveys shutdown cancellation and deadlines, and `defer`
ensures cleanup runs when a function returns. Shutdown uses a fresh context so
the canceled signal context does not immediately abort draining.

HTTP timeouts are 5 seconds for headers, 10 seconds for reads and writes, and
60 seconds for idle connections. These conservative initial values can be
revisited when later endpoints need different behavior.

## Validate

```sh
gofmt -w cmd internal
go vet ./...
go test ./...
```

Tests cover handler status codes, JSON envelopes, headers, configuration,
logging, response failures, and local HTTP serving with cancellation-driven
shutdown. Contributor scope and safety rules are in [AGENTS.md](AGENTS.md).
