#!/bin/bash

# Define the log file location
# give the log file a unique name based on the current timestamp
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
LOG_FILE="$ROOT_DIR/simulator_results_${TIMESTAMP}.log"

if [[ $# -lt 1 ]]; then
    echo "usage: $0 <trace-file>" >&2
    exit 2
fi
TRACE_FILE="$1"

# Navigate to the target directory
cd "$ROOT_DIR" || { echo "Failed to navigate to baseline root"; exit 1; }

# Initialize/clear the log file
echo "--- Starting new benchmark sequence ---" > "$LOG_FILE"

CACHE_SIZES=(
    3241853615
    1624284635
    815959176
    412014790
    209273043
    # 107134436
    # 55093661
)

# tune EthCache LFU levels
THRESHOLD_SETS=(
    "1,3"
    # "1,3,9"
    # "1,3,9,18"
)

# set the level1TTL   = flag.Int64("level1TTL",
TTL=(
    # 0
    # 1000000
    # 2500000
    # 5000000
    # 7500000

    # 10000000
    # 12500000
    # 15000000
    # 20000000
    # 30000000
    10000000
    # 0
)

echo "Running EthCache ..." >> "$LOG_FILE"

for thresholds in "${THRESHOLD_SETS[@]}"; do
   for ttl in "${TTL[@]}"; do
        for size in "${CACHE_SIZES[@]}"; do
            echo "Running LFU with thresholds: $thresholds and TTL: $ttl" >> "$LOG_FILE"
            sudo go run ./cmd/ethcache \
                -trace "$TRACE_FILE" \
                -cache "$size" \
                -startBlock 20500000 \
                -endBlock 20549999 \
                -thresholds "$thresholds" \
                -level1TTL "$ttl" \
                2>&1 | tee >(grep -E "replay complete:|pebble config:|pebble kv-get block-cache:|lfu cache bytes:|lfu cache level bytes:|pebble block cache: " >> "$LOG_FILE")
        done
   done
done
