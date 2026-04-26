#!/usr/bin/env bash
# Smoke test: query logs from Loki to verify agent→server→Loki pipeline.
# Usage: LOKI_URL=http://localhost:3100 HOST_ID=<agent_id> bash examples/queries/logs.sh
set -euo pipefail

LOKI_URL="${LOKI_URL:-http://localhost:3100}"
HOST_ID="${HOST_ID:-}"
LIMIT="${LIMIT:-10}"

echo "=== Praetor M2 Logs Smoke Test ==="
echo "Loki:  ${LOKI_URL}"
if [[ -n "${HOST_ID}" ]]; then
  echo "HostID: ${HOST_ID}"
fi
echo ""

# 1. Check Loki is reachable
echo "Checking Loki health..."
if ! curl -sf "${LOKI_URL}/ready" >/dev/null 2>&1; then
  echo "ERROR: Loki not reachable at ${LOKI_URL}"
  exit 1
fi
echo "OK Loki ready"
echo ""

# 2. Build LogQL query
if [[ -n "${HOST_ID}" ]]; then
  LOGQL="{host_id=\"${HOST_ID}\"}"
else
  LOGQL="{host_id=~\".+\"}"
fi

# Time range: last 15 minutes
START=$(date -u -v-15M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '15 minutes ago' +%Y-%m-%dT%H:%M:%SZ)
END=$(date -u +%Y-%m-%dT%H:%M:%SZ)

echo "Querying logs: ${LOGQL}"
echo "Range: ${START} to ${END}"
echo ""

RESULT=$(curl -sf \
  --get \
  --data-urlencode "query=${LOGQL}" \
  --data-urlencode "start=${START}" \
  --data-urlencode "end=${END}" \
  --data-urlencode "limit=${LIMIT}" \
  --data-urlencode "direction=backward" \
  "${LOKI_URL}/loki/api/v1/query_range" 2>/dev/null || echo "FAILED")

if [[ "${RESULT}" == "FAILED" ]]; then
  echo "ERROR: Loki query failed"
  exit 1
fi

COUNT=$(echo "${RESULT}" | python3 -c "
import json, sys
d = json.load(sys.stdin)
streams = d.get('data', {}).get('result', [])
total = sum(len(s.get('values', [])) for s in streams)
print(total)
" 2>/dev/null || echo "0")

if [[ "${COUNT}" == "0" ]]; then
  echo "WARNING: No log entries found in last 15m."
  echo "  Tip: Ensure agent has journald or syslog access and has been running for >15s."
else
  echo "OK Found ${COUNT} log entries"
  echo ""
  echo "Latest entries:"
  echo "${RESULT}" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for stream in d.get('data', {}).get('result', []):
    labels = stream.get('stream', {})
    for ts, line in stream.get('values', [])[:5]:
        src = labels.get('source', '?')
        print(f'  [{src}] {line[:120]}')
" 2>/dev/null || echo "${RESULT}" | head -30
fi
