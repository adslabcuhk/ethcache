#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "[setup] Restoring DB from golden snapshot..."
sudo rm -rf /mnt/ssd/block-20500000-backup
sudo cp -r /mnt/ssd/goldenDB/block-20500000-backup /mnt/ssd/block-20500000-backup

# Hardcoded parameters.
TRACE_IN="/mnt/lvm_data/FAST-27-THEO/trace/geth-trace-no-cache-enable-snapshot"
PEBBLE_DB="/mnt/ssd/block-20500000-backup/execution/data/geth/chaindata"
TRACE_OUT="/mnt/ssd/50K-no-cache-enable-snapshot-with-valuesize"
START_BLOCK=20500000
END_BLOCK=20549999

if [[ ! -f "$TRACE_IN" ]]; then
	echo "trace file not found: $TRACE_IN" >&2
	exit 1
fi

if [[ ! -d "$PEBBLE_DB" ]]; then
	echo "PebbleDB directory not found: $PEBBLE_DB" >&2
	exit 1
fi

echo "Enriching trace with value sizes..."
echo "  trace in : $TRACE_IN"
echo "  pebbledb : $PEBBLE_DB"
echo "  trace out: $TRACE_OUT"
echo "  block rng: $START_BLOCK .. $END_BLOCK"

go run ./cmd/trace_valuesize_enricher \
	-trace "$TRACE_IN" \
	-db "$PEBBLE_DB" \
	-out "$TRACE_OUT" \
	-startBlock "$START_BLOCK" \
	-endBlock "$END_BLOCK" \
	-missing-value-size -1

echo "Done: $TRACE_OUT"
