#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
LOG_FILE="$ROOT_DIR/trace_size_results_${TIMESTAMP}.log"
TRACE_FILE="${TRACE_FILE:-/mnt/ssd/filtered-50K-no-cache-enable-snapshot-with-valuesize}"

cd "$ROOT_DIR"
echo "--- Starting trace-size benchmark sequence ---" > "$LOG_FILE"

CACHE_SIZES=(815959176)
END_BLOCKS=(20509999 20519999 20529999 20539999)

run_trace_sweep() {
    local command="$1"
    local label="$2"
    local grep_pattern="$3"

    echo "Running $label trace-size sweep ..." >> "$LOG_FILE"
    for cache_bytes in "${CACHE_SIZES[@]}"; do
        for end_block in "${END_BLOCKS[@]}"; do
            echo "Running $label cache=$cache_bytes endBlock=$end_block" >> "$LOG_FILE"
            sudo go run "./cmd/$command" \
                -trace "$TRACE_FILE" \
                -cache "$cache_bytes" \
                -startBlock 20500000 \
                -endBlock "$end_block" \
                2>&1 | tee >(grep -E "$grep_pattern" >> "$LOG_FILE")
        done
    done
}

run_trace_sweep lirs "LIRS" "replay complete:|lirs cache stats:"
run_trace_sweep rawlru "rawLRU" "replay complete:|raw lru stats:"
run_trace_sweep ethcache "EthCache" "replay complete:|lfu cache bytes:|lfu cache level bytes:"
run_trace_sweep s3fifo "S3FIFO" "replay complete:|s3fifo cache stats:"
run_trace_sweep arc "ARC" "replay complete:|arc cache bytes:"
run_trace_sweep gethcache "GethCache" "replay complete:|cache stats: class="

echo "Trace-size runs finished. Log: $LOG_FILE"
