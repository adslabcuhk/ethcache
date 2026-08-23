#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
LOG_FILE="$ROOT_DIR/simulator_results_${TIMESTAMP}.log"
TRACE_FILE="${TRACE_FILE:-/mnt/ssd/filtered-50K-no-cache-enable-snapshot-with-valuesize}"

cd "$ROOT_DIR"
echo "--- Starting retained benchmark sequence ---" > "$LOG_FILE"

CACHE_SIZES=(
    3241853615
    1624284635
    412014790
    209273043
    107134436
    55093661
)

run_cache() {
    local command="$1"
    local label="$2"
    local grep_pattern="$3"
    shift 3

    echo "Running $label ..." >> "$LOG_FILE"
    for cache_bytes in "${CACHE_SIZES[@]}"; do
        echo "Running $label with cache size: $cache_bytes" >> "$LOG_FILE"
        sudo go run "./cmd/$command" \
            -trace "$TRACE_FILE" \
            -cache "$cache_bytes" \
            -startBlock 20500000 \
            -endBlock 20549999 \
            "$@" \
            2>&1 | tee >(grep -E "$grep_pattern" >> "$LOG_FILE")
    done
}

for thresholds in "1,3"; do
    for ttl in 12500000; do
        run_cache ethcache "EthCache thresholds=$thresholds ttl=$ttl" \
            "replay complete:|lfu cache bytes:|lfu cache level bytes:" \
            -thresholds "$thresholds" -level1TTL "$ttl"
    done
done

run_cache gethcache "GethCache" "replay complete:|cache stats: class="
run_cache rawlru "rawLRU" "replay complete:|raw lru stats:"
run_cache s3fifo "S3FIFO" "replay complete:|s3fifo cache stats:"
run_cache arc "ARC" "replay complete:|arc cache bytes:"
run_cache lirs "LIRS" "replay complete:|lirs cache stats:"

echo "All retained benchmark runs finished. Log: $LOG_FILE"
