#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$ROOT_DIR/bin"

mkdir -p "$BIN_DIR"
cd "$ROOT_DIR"

commands=(
	ethcache
	gethcache
	rawlru
	s3fifo
	arc
	lirs
)

for command in "${commands[@]}"; do
	echo "Building $command..."
	go build -o "$BIN_DIR/$command" "./cmd/$command"
done

echo "Build complete. Binaries are in $BIN_DIR"