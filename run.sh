#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$#" -lt 1 ]; then
    echo "usage: $0 <ethcache|gethcache|rawlru|s3fifo|arc|lirs> [args...]" >&2
    exit 2
fi

command="$1"
shift

case "$command" in
    ethcache|gethcache|rawlru|s3fifo|arc|lirs)
        cd "$ROOT_DIR"
        go run "./cmd/$command" "$@"
        ;;
    *)
        echo "unknown command: $command" >&2
        echo "valid commands: ethcache gethcache rawlru s3fifo arc lirs" >&2
        exit 2
        ;;
esac