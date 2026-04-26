# Praetor M2 Query Examples

Developer smoke tests for verifying the M2 observability pipeline.

## Prerequisites

- Praetor stack running: `make compose-up && make migrate-up`
- Server running: `go run ./cmd/praetor-server --config examples/server.yaml`
- At least one agent enrolled and sending data

## Metrics (VictoriaMetrics)

```sh
# Check all metrics from all agents
bash examples/queries/metrics.sh

# Check specific agent
HOST_ID=<your-agent-id> bash examples/queries/metrics.sh

# Check specific metric
METRIC=memory_used_percent HOST_ID=<id> bash examples/queries/metrics.sh
```

Available metrics: `cpu_usage_percent`, `memory_used_percent`, `memory_used_bytes`,
`memory_total_bytes`, `disk_used_percent`, `disk_used_bytes`, `net_bytes_sent`,
`net_bytes_recv`

All metrics have a `host_id` label for per-host filtering.

## Logs (Loki)

```sh
# Last 10 log entries from all agents
bash examples/queries/logs.sh

# From specific agent
HOST_ID=<your-agent-id> bash examples/queries/logs.sh

# More entries
LIMIT=50 HOST_ID=<id> bash examples/queries/logs.sh
```

## Config push

```sh
# Update heartbeat interval for a host
./bin/praetor-server config set <host-id> heartbeat_interval_seconds=60 \
  --config examples/server.yaml

# Verify
./bin/praetor-server config get <host-id> --config examples/server.yaml
```
