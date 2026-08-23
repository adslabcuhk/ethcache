#!/usr/bin/env python3

import argparse
import csv
import os
import subprocess
import sys
from datetime import datetime


# DEFAULT_TRACE_PATH = "/mnt/lvm_data/FAST-26-EthAnalysis/Traces/geth-trace-without-cache-merged-block-20500000-21500000"
DEFAULT_TRACE_PATH = "/mnt/ssd/50K-no-cache-enable-snapshot-with-valuesize"
DEFAULT_THRESHOLD_SETS = "1,4,9,18,36;1,4;1,4,9;1,4,9,18;1,5,9,18,36;1,3,9,18,36"

def parse_threshold_sets(raw: str):
    sets = []
    for chunk in raw.split(";"):
        s = chunk.strip()
        if not s:
            continue
        vals = []
        for part in s.split(","):
            part = part.strip()
            if not part:
                continue
            vals.append(int(part))
        if vals:
            sets.append(vals)
    if not sets:
        raise ValueError("no valid threshold sets parsed")
    return sets


def threshold_label(values):
    return "-".join(str(v) for v in values)


def ensure_parent(path):
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)


def run_cmd(cmd, cwd):
    proc = subprocess.run(cmd, cwd=cwd, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"command failed ({proc.returncode}): {' '.join(cmd)}")


def read_run_summary(path):
    with open(path, "r", newline="") as f:
        rows = list(csv.DictReader(f))
    if not rows:
        raise RuntimeError(f"run summary is empty: {path}")
    return rows[-1]


def append_master_row(path, fieldnames, row):
    ensure_parent(path)
    exists = os.path.exists(path)
    with open(path, "a", newline="") as f:
        w = csv.DictWriter(f, fieldnames=fieldnames)
        if not exists:
            w.writeheader()
        w.writerow(row)


def main():
    p = argparse.ArgumentParser(description="Automate EthCache threshold tuning using cmd/ethcache")
    p.add_argument("--workspace", default="/home/jzhao/EthCaching/baseline")
    p.add_argument("--trace", default=DEFAULT_TRACE_PATH)
    p.add_argument("--cache", type=int, default=815959176)
    p.add_argument("--start-block", type=int, default=20500000)
    p.add_argument("--end-block", type=int, default=20549999)
    p.add_argument("--interval-ops", type=int, default=100000)
    p.add_argument(
        "--threshold-sets",
        default=DEFAULT_THRESHOLD_SETS,
        help="semicolon-separated threshold sets; comma-separated values per set",
    )
    p.add_argument("--out-dir", default="tuning_runs")
    p.add_argument("--master", default="")
    args = p.parse_args()

    ws = os.path.abspath(args.workspace)

    threshold_sets = parse_threshold_sets(args.threshold_sets)

    os.makedirs(os.path.join(ws, args.out_dir), exist_ok=True)

    stamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    master_rel_path = args.master.strip() if args.master else ""
    if not master_rel_path:
        master_rel_path = os.path.join(args.out_dir, f"master_results_{stamp}.csv")
    master_abs_path = os.path.join(ws, master_rel_path)

    fieldnames = [
        "run_id",
        "thresholds",
        "cache",
        "start_block",
        "end_block",
        "get_total",
        "get_hits",
        "get_hit_rate",
        "get_latency_avg_ms",
        "get_latency_p95_ms",
        "put_total",
        "failures",
        "promotions_total",
        "evictions_total",
        "evictions_by_level",
        "evicted_bytes_by_level",
        "level_entries_end",
        "level_bytes_end",
        "run_summary_path",
        "interval_summary_path",
    ]

    best = None
    for idx, thresholds in enumerate(threshold_sets, start=1):
        label = threshold_label(thresholds)
        run_id = f"{stamp}_t{idx}_{label}"
        run_summary_path = os.path.join(ws, args.out_dir, f"{run_id}_run_summary.csv")
        interval_summary_path = os.path.join(ws, args.out_dir, f"{run_id}_interval_summary.csv")

        cmd = [
            "go",
            "run",
            "./cmd/ethcache",
            "-trace",
            args.trace,
            "-cache",
            str(args.cache),
            "-startBlock",
            str(args.start_block),
            "-endBlock",
            str(args.end_block),
            "-metricsMode",
            "summary",
            "-thresholds",
            ",".join(str(v) for v in thresholds),
            "-runSummary",
            run_summary_path,
            "-intervalSummary",
            interval_summary_path,
            "-intervalOps",
            str(args.interval_ops),
        ]

        print(f"[{idx}/{len(threshold_sets)}] running thresholds={thresholds}")
        run_cmd(cmd, ws)

        row = read_run_summary(run_summary_path)
        out_row = {
            "run_id": run_id,
            "thresholds": row.get("thresholds_normalized", ""),
            "cache": str(args.cache),
            "start_block": str(args.start_block),
            "end_block": str(args.end_block),
            "get_total": row.get("get_total", ""),
            "get_hits": row.get("get_hits", ""),
            "get_hit_rate": row.get("get_hit_rate", ""),
            "get_latency_avg_ms": row.get("get_latency_avg_ms", ""),
            "get_latency_p95_ms": row.get("get_latency_p95_ms", ""),
            "put_total": row.get("put_total", ""),
            "failures": row.get("failures", ""),
            "promotions_total": row.get("promotions_total", ""),
            "evictions_total": row.get("evictions_total", ""),
            "evictions_by_level": row.get("evictions_by_level", ""),
            "evicted_bytes_by_level": row.get("evicted_bytes_by_level", ""),
            "level_entries_end": row.get("level_entries_end", ""),
            "level_bytes_end": row.get("level_bytes_end", ""),
            "run_summary_path": run_summary_path,
            "interval_summary_path": interval_summary_path,
        }
        append_master_row(master_abs_path, fieldnames, out_row)

        try:
            hr = float(out_row["get_hit_rate"])
        except ValueError:
            hr = -1.0

        if best is None or hr > best[0]:
            best = (hr, out_row)

    if best is None:
        print("No successful runs", file=sys.stderr)
        sys.exit(2)

    print("Best threshold set:")
    print(f"  thresholds={best[1]['thresholds']}")
    print(f"  get_hit_rate={best[1]['get_hit_rate']}")
    print(f"  run_summary={best[1]['run_summary_path']}")
    print(f"  interval_summary={best[1]['interval_summary_path']}")
    print(f"  master={master_abs_path}")


if __name__ == "__main__":
    main()
