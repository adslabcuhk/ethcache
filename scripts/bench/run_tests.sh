#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
LOG_FILE="$ROOT_DIR/replay_results_${TIMESTAMP}.log"
TRACE_FILE="${TRACE_FILE:-/mnt/lvm_data/FAST-26-EthAnalysis/Traces/geth-trace-without-cache-merged-block-20500000-21500000}"

restore_db_state() {
    echo "[setup] Restoring DB from golden snapshot..."
    sudo rm -rf /mnt/ssd/block-20500000-backup
    sudo cp -r /mnt/ssd/goldenDB/block-20500000-backup /mnt/ssd/block-20500000-backup
}

drop_system_caches() {
    echo "[setup] Dropping Linux page cache..."
    sudo sync
    echo 3 | sudo tee /proc/sys/vm/drop_caches > /dev/null
}

prepare_run_with_db_restore() {
    restore_db_state
    drop_system_caches
}

cd "$ROOT_DIR"
echo "--- Starting replay benchmark sequence ---" > "$LOG_FILE"

DB_CACHE_SIZES=(67108864 33554432)
CACHE_SIZES=(745304125 778858557)

for i in "${!DB_CACHE_SIZES[@]}"; do
    db_cache_size="${DB_CACHE_SIZES[$i]}"
    cache_size="${CACHE_SIZES[$i]}"
    echo "Running EthCache with cache=${cache_size}, dbCacheSize=${db_cache_size}" | tee -a "$LOG_FILE"

    prepare_run_with_db_restore || { echo "Run setup failed"; exit 1; }

    sudo go run ./cmd/pebble-ethcache-tuning \
        -trace "$TRACE_FILE" \
        -cache "$cache_size" \
        -dbCacheSize "$db_cache_size" \
        -startBlock 20500000 \
        -endBlock 20510000 \
        2>&1 | tee >(grep -E "replay complete:|pebble config:|pebble kv-get block-cache:|lfu cache bytes:|lfu cache level bytes:|pebble block cache:" >> "$LOG_FILE")
done

echo "Replay benchmark runs finished. Log: $LOG_FILE"
