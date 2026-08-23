# EthCaching Baseline Runners

This module contains the trace replay runners for EthCache and the retained cache baselines. The runnable programs now use the standard Go multi-command layout: each binary has its own `main.go` under `cmd/<name>/`, and the cache implementations live in importable packages at the module root.

## Layout

```text
baseline/
	cmd/
		ethcache/                  # proposed EthCache runner
		gethcache/                 # Geth-style scattered LRU cache runner
		rawlru/                    # single raw byte-tracked LRU baseline
		s3fifo/                    # S3-FIFO baseline
		arc/                       # ARC baseline
		lirs/                      # LIRS baseline
	ethcache/                    # EthCache implementation
	gethcache/                   # GethCache implementation and per-class LRU manager
	arc/ lirs/ s3fifo/           # retained baseline implementations
	scripts/
		bench/                     # benchmark/replay shell scripts
		tune/                      # EthCache tuning shell/Python scripts
		analysis/                  # result analysis scripts
		lib/                       # shared shell helpers
```

The removed active baselines are SIEVE, correlation LFU/LRU, SLRU, and PLFU.

## Commands

Run from this directory unless otherwise noted.

Build all retained replay binaries:

```bash
./build.sh
```

This writes binaries to `bin/`:

```text
bin/ethcache
bin/gethcache
bin/rawlru
bin/s3fifo
bin/arc
bin/lirs
```

Run a command through the wrapper:

```bash
./run.sh ethcache -trace <trace-file> -cache <bytes> -startBlock <block> -endBlock <block>
```

Or run directly with Go:

```bash
go run ./cmd/ethcache -trace <trace-file> -cache <bytes> -startBlock <block> -endBlock <block>
```

## EthCache

EthCache is the proposed cache design. It is implemented in `ethcache/` and run by `cmd/ethcache`.

Example:

```bash
go run ./cmd/ethcache \
	-trace /mnt/ssd/filtered-50K-no-cache-enable-snapshot-with-valuesize \
	-cache 815959176 \
	-startBlock 20500000 \
	-endBlock 20549999 \
	-thresholds 1,3 \
	-level1TTL 10000000
```

Useful EthCache flags:

- `-thresholds`: comma-separated frequency thresholds, for example `1,3` or `1,4,6,12,24`.
- `-level1TTL`: demotion TTL in GET ticks for level-1 entries. Values `<=0` disable TTL demotion.
- `-metricsMode`: `summary` or `full`.
- `-runSummary`: run-level CSV output path.
- `-intervalSummary`: interval CSV output path.
- `-intervalOps`: operations per interval row.

## Retained Baselines

GethCache is the Geth-style scattered LRU cache runner. It is implemented in `gethcache/` and run by `cmd/gethcache`:

```bash
go run ./cmd/gethcache \
	-trace <trace-file> \
	-cache 1073741824 \
	-divide 1 \
	-multiply 1 \
	-lowerCacheBytes 536870912 \
	-startBlock 20500000 \
	-endBlock 20549999
```

rawLRU is a single byte-tracked LRU baseline:

```bash
go run ./cmd/rawlru -trace <trace-file> -cache <bytes> -startBlock <block> -endBlock <block>
```

S3FIFO, ARC, and LIRS use the same common replay flags:

```bash
go run ./cmd/s3fifo -trace <trace-file> -cache <bytes> -startBlock <block> -endBlock <block>
go run ./cmd/arc    -trace <trace-file> -cache <bytes> -startBlock <block> -endBlock <block>
go run ./cmd/lirs   -trace <trace-file> -cache <bytes> -startBlock <block> -endBlock <block>
```

## Scripts

Benchmark scripts are under `scripts/bench/`:

- `simu_run.sh`: retained-suite sweep for EthCache, GethCache, rawLRU, S3FIFO, ARC, and LIRS.
- `tune_trace_size.sh`: retained-suite sweep over several end blocks.
- `new_trace.sh`: trace helper script.

EthCache tuning scripts are under `scripts/tune/`:

- `tune_cache_size.sh`
- `tune_tier_threshold.sh`
- `tune_ttl.sh`
- `tune_thresholds.py`

Analysis scripts are under `scripts/analysis/`.

Most shell scripts accept `TRACE_FILE` from the environment, for example:

```bash
TRACE_FILE=/path/to/trace ./scripts/bench/simu_run.sh
```

Some benchmark scripts restore `/mnt/ssd/block-20500000-backup` from `/mnt/ssd/goldenDB/block-20500000-backup` and drop Linux page cache, so they require `sudo` and the expected external data paths.

## Outputs

Common outputs include:

- `run_summary.csv`: run-level summary for runners that emit CSV summaries.
- `interval_summary.csv`: interval-level summary for runners that emit interval metrics.
- `cache_metrics.csv`: full per-operation metrics when `-metricsMode full` is enabled.
- `*_results_<timestamp>.log`: script-level benchmark logs.

CSV and log outputs are ignored by Git.

## Validation

The retained command build surface is:

```bash
go build ./cmd/ethcache ./cmd/gethcache ./cmd/rawlru ./cmd/s3fifo ./cmd/arc ./cmd/lirs
```

Package tests for the retained standalone policies can be run with:

```bash
go test ./ethcache ./arc ./lirs ./s3fifo
```

`go test ./...` currently also enters legacy analysis directories and root-level helper programs that contain multiple `main` functions, so use the focused commands above when validating the replay runners.
