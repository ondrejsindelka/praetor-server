# praetor-server

> Status: **pre-alpha — M0 scaffolding only**

The praetor-server is the Go control plane for Praetor — a self-hosted
observability and security platform with a native MCP interface for LLM agents.

## Architecture

```
         LLM Agent (Claude Desktop / Claude Code / custom)
                         │ MCP (HTTP/SSE)
                         ▼
                 praetor-mcp (TypeScript)
                         │ HTTP
                         ▼
       ┌─────────────────────────────────────────┐
       │             praetor-server              │
       │                                         │
       │   ┌──────────────┐  ┌───────────────┐  │
       │   │  gRPC :8443  │  │  REST  :8080  │  │
       │   │  Enroll RPC  │  │  /v1/hosts    │  │
       │   │  Connect RPC │  │  /v1/commands │  │
       │   └──────────────┘  └───────────────┘  │
       │                                         │
       │   ┌────────────┐  ┌─────────────────┐  │
       │   │  Postgres  │  │ VictoriaMetrics │  │
       │   │ state+audit│  │    (metrics)    │  │
       │   └────────────┘  └─────────────────┘  │
       │                   ┌─────────────────┐  │
       │                   │      Loki       │  │
       │                   │     (logs)      │  │
       │                   └─────────────────┘  │
       └─────────────────────────────────────────┘
                         ▲
                         │ bidirectional gRPC over mTLS
               ┌─────────────────────┐
               │   praetor-agent     │  (one per monitored host)
               │  collectors: metrics│
               │  logs · security    │
               │  command executor   │
               └─────────────────────┘
```

## Build

```sh
make build      # local binary → bin/praetor-server
make test       # run tests with race detector
make lint       # golangci-lint
make run-dev    # go run against examples/server.yaml
make clean      # remove bin/, coverage.out
```

Prerequisites: Go 1.22+, `golangci-lint` for `make lint`.

## Configuration

Copy `examples/server.yaml` and edit:

```yaml
grpc_listen: :8443                                           # gRPC ingress for agents (mTLS)
http_listen: :8080                                           # REST API for praetor-mcp and UI
postgres_dsn: postgres://praetor:praetor@localhost:5432/praetor?sslmode=disable
victoriametrics_url: http://localhost:8428                   # TSDB for metrics
loki_url: http://localhost:3100                              # log storage
```

`postgres_dsn`, `victoriametrics_url`, and `loki_url` are required.
`grpc_listen` defaults to `:8443`; `http_listen` defaults to `:8080`.

## Milestones

- **M0** (current) — scaffolding: module layout, config loader, placeholder
  main with signal handling, Makefile, CI. No gRPC, no DB, no business logic.
- **M1** — walking skeleton: Enroll + Connect gRPC handlers, Postgres schema
  (`hosts`, `agent_identities`, `enrollment_tokens`), REST `GET /v1/hosts`.
- See the [project roadmap](https://github.com/ondrejsindelka/praetor) for
  full milestone details.
