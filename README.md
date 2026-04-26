# praetor-server

[![CI](https://github.com/ondrejsindelka/praetor-server/actions/workflows/ci.yml/badge.svg)](https://github.com/ondrejsindelka/praetor-server/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ondrejsindelka/praetor-server)](https://github.com/ondrejsindelka/praetor-server/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/ondrejsindelka/praetor-server)](https://goreportcard.com/report/github.com/ondrejsindelka/praetor-server)

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

## Dev stack

Start the full dev stack (Postgres, VictoriaMetrics, Loki, Grafana):

```sh
make compose-up
```

Apply database migrations:

```sh
make migrate-up
```

Check migration status:

```sh
make migrate-status
```

Stop the dev stack:

```sh
make compose-down
```

Services:
- Postgres: `localhost:5432` (user/pass/db: `praetor`)
- VictoriaMetrics: `http://localhost:8428`
- Loki: `http://localhost:3100`
- Grafana: `http://localhost:3000` (anonymous admin, no login required)

## Issuing enrollment tokens

Before an agent can connect, issue an enrollment token:

```sh
make token-issue LABEL="prod-db cluster"
```

Or with explicit flags:

```sh
./bin/praetor-server token issue \
  --label "prod-db cluster" \
  --ttl 15m \
  --config examples/server.yaml
```

List active tokens:

```sh
./bin/praetor-server token list --config examples/server.yaml
```

Revoke a token:

```sh
./bin/praetor-server token revoke <id> --config examples/server.yaml
```

## CA initialization

On first start, `praetor-server` creates a root CA and server certificate in `data_dir/ca/`
(default `./tmp/data/ca/`). This directory contains the **root CA private key** — back it up
to offline storage. If lost, all agent certificates must be reissued.

> **Security note:** `data_dir/ca/root.key` has mode `0400`. Never commit it to version control.

## Milestones

- **M0** (current) — scaffolding: module layout, config loader, placeholder
  main with signal handling, Makefile, CI. No gRPC, no DB, no business logic.
- **M1** — walking skeleton: Enroll + Connect gRPC handlers, Postgres schema
  (`hosts`, `agent_identities`, `enrollment_tokens`), REST `GET /v1/hosts`.
- See the [project roadmap](https://github.com/ondrejsindelka/praetor) for
  full milestone details.
