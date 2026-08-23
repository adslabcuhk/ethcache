#!/usr/bin/env bash
set -euo pipefail

# Simple runner that builds and runs the small runner which calls utils.ProcessLogBatch.
# It is safe to run even if the large trace file is missing (it will be skipped).

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "Running sim runner (go test_utils test_utils.go)"
go test_utils test_utils.go "$@"