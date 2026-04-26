#!/usr/bin/env bash
# Smoke test: query a metric from VictoriaMetrics to verify agent→server→VM pipeline.
# Usage: VM_URL=http://localhost:8428 HOST_ID=<agent_id> bash examples/queries/metrics.sh
set -euo pipefail

VM_URL="${VM_URL:-http://localhost:8428}"
HOST_ID="${HOST_ID:-}"
METRIC="${METRIC:-cpu_usage_percent}"

echo "=== Praetor M2 Metrics Smoke Test ==="
echo "VM:     ${VM_URL}"
echo "Metric: ${METRIC}"
if [[ -n "${HOST_ID}" ]]; then
  echo "HostID: ${HOST_ID}"
fi
echo ""

# 1. Check VM is reachable
echo "Checking VM health..."
if ! curl -sf "${VM_URL}/health" >/dev/null 2>&1; then
  echo "ERROR: VM not reachable at ${VM_URL}"
  exit 1
fi
echo "OK VM healthy"
echo ""

# 2. Query the metric (last 5 minutes)
echo "Querying ${METRIC} (last 5m)..."
QUERY="${METRIC}"
if [[ -n "${HOST_ID}" ]]; then
  QUERY="{host_id=\"${HOST_ID}\"}"
fi

RESULT=$(curl -sf "${VM_URL}/api/v1/query?query=${QUERY}&time=$(date +%s)" 2>/dev/null || echo "FAILED")

if [[ "${RESULT}" == "FAILED" ]]; then
  echo "ERROR: VM query failed"
  exit 1
fi

COUNT=$(echo "${RESULT}" | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d.get('data',{}).get('result',[])))" 2>/dev/null || echo "0")

if [[ "${COUNT}" == "0" ]]; then
  echo "WARNING: No results found. Is the agent running and sending metrics?"
  echo "  Tip: Run the agent and wait ~15s for the first MetricBatch."
else
  echo "OK Found ${COUNT} metric series"
  echo ""
  echo "Sample data:"
  echo "${RESULT}" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for r in d.get('data', {}).get('result', [])[:5]:
    print(f\"  {r.get('metric', {})}  value={r.get('value', ['',''])[1]}\")
" 2>/dev/null || echo "${RESULT}" | head -20
fi
