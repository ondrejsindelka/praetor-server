# Rez 01 — Watchdog Schema + Storage + Crypto

## Scope

Storage-only slice: no business logic, no API endpoints, no eval loop.

## Plan

1. **Migration 00007** — 7 new tables: playbooks, rules, rule_state, investigations, schedules, llm_providers, webhooks.
2. **Crypto helper** — AES-GCM encrypt/decrypt for secrets at rest (api_key_enc, secret_enc). Master key from `PRAETOR_MASTER_KEY` env var, base64-encoded 32 bytes.
3. **Storage interfaces** — `internal/watchdog/storage/interfaces.go` with typed structs + repo interfaces for all 7 entities.
4. **Postgres implementations** — one file per repo in `internal/watchdog/storage/postgres/`, using pgx/v5 + json.Marshal/Unmarshal for JSONB. Fleet isolation on every query.
5. **Integration tests** — testcontainers-go, build tag `integration`. Covers CRUD roundtrips, fleet isolation, investigation filters.
6. **Config + CLI** — `WatchdogEnabled bool` in config, `PRAETOR_MASTER_KEY` validation at startup, `generate-master-key` subcommand.

## Key constraints

- `pgx/v5` only — no lib/pq.
- `watchdog_enabled: false` is the default — server starts without PRAETOR_MASTER_KEY.
- API keys / master key never logged.
- All commits on `watchdog/rez-01-server-storage`, never on main.
