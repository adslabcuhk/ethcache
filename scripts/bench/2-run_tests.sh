#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
LOG_FILE="$ROOT_DIR/pebble_ethcache_results_${TIMESTAMP}.log"
TRACE_FILE="${TRACE_FILE:-/mnt/lvm_data/FAST-26-EthAnalysis/Traces/geth-trace-without-cache-merged-block-20500000-21500000}"

cd "$ROOT_DIR"
echo "--- Starting Pebble EthCache tuning run ---" > "$LOG_FILE"

sudo go run ./cmd/pebble-ethcache-tuning \
    -trace "$TRACE_FILE" \
    -cache 278921216 \
    -dbCacheSize 1073741824 \
    -startBlock 20500000 \
    -endBlock 20510000 \
    2>&1 | tee >(grep -E "replay complete:|pebble config:|pebble kv-get block-cache:|lfu cache bytes:|lfu cache level bytes:|pebble block cache:" >> "$LOG_FILE")

echo "Pebble EthCache run finished. Log: $LOG_FILE"
