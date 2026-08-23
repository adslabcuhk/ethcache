#!/usr/bin/env python3

import argparse
import csv
import math
import os
import statistics
from glob import glob


def to_float(value, default=0.0):
    try:
        return float(value)
    except (TypeError, ValueError):
        return default


def to_int(value, default=0):
    try:
        return int(float(value))
    except (TypeError, ValueError):
        return default


def pick(row, names, default=""):
    for name in names:
        if name in row and row[name] not in (None, ""):
            return row[name]
    return default


def resolve_artifact_path(base_dir, stage_dir, raw_path):
    candidates = []
    if os.path.isabs(raw_path):
        candidates.append(raw_path)
        candidates.append(os.path.join(stage_dir, os.path.basename(raw_path)))
    else:
        candidates.append(os.path.join(base_dir, raw_path))
        candidates.append(os.path.join(stage_dir, os.path.basename(raw_path)))

    for path in candidates:
        if os.path.exists(path):
            return path

    raise FileNotFoundError(f"artifact not found: {raw_path}")


def parse_thresholds(value):
    try:
        return [int(v) for v in value.split("|")]
    except Exception:
        return []


def find_master_csv(stage_dir, explicit_master):
    if explicit_master:
        return explicit_master

    matches = sorted(glob(os.path.join(stage_dir, "master_results*.csv")))
    if not matches:
        raise FileNotFoundError(f"no master_results*.csv found under {stage_dir}")
    return matches[-1]


def load_stage2_rows(base_dir, stage_dir, master_csv):
    with open(master_csv, "r", newline="") as f:
        rows = list(csv.DictReader(f))

    analyzed = []
    for row in rows:
        interval_csv = resolve_artifact_path(base_dir, stage_dir, row.get("interval_summary_path", ""))
        run_csv = resolve_artifact_path(base_dir, stage_dir, row.get("run_summary_path", ""))

        interval_hit_rates = []
        interval_lat_avg = []
        interval_churn = []
        cross_moves = []
        same_refresh = []

        with open(interval_csv, "r", newline="") as f2:
            for r in csv.DictReader(f2):
                interval_hit_rates.append(
                    to_float(pick(r, ["interval_hit_rate", "get_hit_rate_interval"]))
                )
                interval_lat_avg.append(
                    to_float(pick(r, ["get_latency_avg_ms", "get_latency_avg_ms_interval"]))
                )
                interval_churn.append(
                    to_float(pick(r, ["churn_index", "churn_ratio_interval"]))
                )
                cross_moves.append(
                    to_int(pick(r, ["cross_level_promotions_interval", "cross_level_promotions"]))
                )
                same_refresh.append(
                    to_int(pick(r, ["same_level_refresh_interval", "same_level_refresh"]))
                )

        n = len(interval_hit_rates)
        hit_rate = to_float(row.get("get_hit_rate", 0))
        lat_avg = to_float(row.get("get_latency_avg_ms", 0))
        lat_p95 = to_float(row.get("get_latency_p95_ms", 0))

        analyzed.append(
            {
                "run_id": row.get("run_id", ""),
                "thresholds": row.get("thresholds", row.get("thresholds_normalized", "")),
                "get_hit_rate": hit_rate,
                "get_latency_avg_ms": lat_avg,
                "get_latency_p95_ms": lat_p95,
                "get_total": to_int(row.get("get_total", 0)),
                "get_hits": to_int(row.get("get_hits", 0)),
                "evictions_total": to_int(row.get("evictions_total", 0)),
                "promotions_total": to_int(row.get("promotions_total", 0)),
                "cross_level_promotions_total": to_int(row.get("cross_level_promotions_total", 0)),
                "same_level_refresh_total": to_int(row.get("same_level_refresh_total", 0)),
                "interval_count": n,
                "hr_std": statistics.pstdev(interval_hit_rates) if n > 1 else 0.0,
                "hr_min": min(interval_hit_rates) if n else 0.0,
                "hr_max": max(interval_hit_rates) if n else 0.0,
                "lat_interval_mean": statistics.mean(interval_lat_avg) if interval_lat_avg else 0.0,
                "lat_interval_p95": (
                    sorted(interval_lat_avg)[max(0, math.ceil(0.95 * len(interval_lat_avg)) - 1)]
                    if interval_lat_avg
                    else 0.0
                ),
                "churn_mean": statistics.mean(interval_churn) if interval_churn else 0.0,
                "churn_p95": (
                    sorted(interval_churn)[max(0, math.ceil(0.95 * len(interval_churn)) - 1)]
                    if interval_churn
                    else 0.0
                ),
                "cross_moves_interval_total": sum(cross_moves),
                "same_refresh_interval_total": sum(same_refresh),
                "run_summary_path": run_csv,
                "interval_summary_path": interval_csv,
            }
        )

    return analyzed


def print_rankings(rows):
    ranked = sorted(
        rows,
        key=lambda r: (
            -r["get_hit_rate"],
            r["get_latency_avg_ms"],
            r["churn_mean"],
        ),
    )

    print("TOP BY GET HIT RATE")
    for idx, r in enumerate(ranked, start=1):
        print(
            f"{idx}. th={r['thresholds']} "
            f"hit={r['get_hit_rate']:.6f} "
            f"lat_avg={r['get_latency_avg_ms']:.6f} "
            f"lat_p95={r['get_latency_p95_ms']:.6f} "
            f"churn_mean={r['churn_mean']:.6f} "
            f"churn_p95={r['churn_p95']:.6f} "
            f"hr_std={r['hr_std']:.6f}"
        )

    best = ranked[0]
    print("\nDELTA VS BEST")
    for r in ranked[1:]:
        print(
            f"th={r['thresholds']} "
            f"d_hit={r['get_hit_rate'] - best['get_hit_rate']:+.6f} "
            f"d_lat={r['get_latency_avg_ms'] - best['get_latency_avg_ms']:+.6f} "
            f"d_churn={r['churn_mean'] - best['churn_mean']:+.6f}"
        )

    return ranked


def print_one_knob_sensitivity(rows, anchor_thresholds):
    anchor = None
    for r in rows:
        if r["thresholds"] == anchor_thresholds:
            anchor = r
            break

    if anchor is None:
        print(f"\nANCHOR {anchor_thresholds} not found in this stage set")
        return

    base = parse_thresholds(anchor["thresholds"])
    print(f"\nONE-KNOB SENSITIVITY FROM {anchor_thresholds}")
    for r in rows:
        if r is anchor:
            continue
        cur = parse_thresholds(r["thresholds"])
        if len(cur) != len(base) or not cur:
            continue
        delta = [cur[i] - base[i] for i in range(len(base))]
        if sum(1 for d in delta if d != 0) == 1:
            print(
                f"th={r['thresholds']} delta={delta} "
                f"d_hit={r['get_hit_rate'] - anchor['get_hit_rate']:+.6f} "
                f"d_lat={r['get_latency_avg_ms'] - anchor['get_latency_avg_ms']:+.6f} "
                f"d_churn={r['churn_mean'] - anchor['churn_mean']:+.6f}"
            )


def print_pareto(rows):
    pareto = []
    for r in rows:
        dominated = False
        for o in rows:
            if o is r:
                continue
            better_or_equal = (
                o["get_hit_rate"] >= r["get_hit_rate"]
                and o["get_latency_avg_ms"] <= r["get_latency_avg_ms"]
                and o["churn_mean"] <= r["churn_mean"]
            )
            strictly = (
                o["get_hit_rate"] > r["get_hit_rate"]
                or o["get_latency_avg_ms"] < r["get_latency_avg_ms"]
                or o["churn_mean"] < r["churn_mean"]
            )
            if better_or_equal and strictly:
                dominated = True
                break
        if not dominated:
            pareto.append(r)

    print("\nPARETO FRONT (maximize hit, minimize latency/churn)")
    for r in sorted(pareto, key=lambda x: -x["get_hit_rate"]):
        print(
            f"th={r['thresholds']} "
            f"hit={r['get_hit_rate']:.6f} "
            f"lat_avg={r['get_latency_avg_ms']:.6f} "
            f"churn_mean={r['churn_mean']:.6f}"
        )


def print_churn_breakdown(rows):
    print("\nCROSS-LEVEL VS SAME-LEVEL ACTIVITY")
    for r in sorted(rows, key=lambda x: -x["get_hit_rate"]):
        refresh = r["same_refresh_interval_total"]
        ratio = float("inf") if refresh <= 0 else (r["cross_moves_interval_total"] / refresh)
        ratio_str = "inf" if math.isinf(ratio) else f"{ratio:.6f}"
        print(
            f"th={r['thresholds']} "
            f"cross={r['cross_moves_interval_total']} "
            f"refresh={refresh} "
            f"cross_refresh_ratio={ratio_str}"
        )


def maybe_write_csv(rows, output_csv):
    if not output_csv:
        return

    fieldnames = [
        "run_id",
        "thresholds",
        "get_hit_rate",
        "get_latency_avg_ms",
        "get_latency_p95_ms",
        "churn_mean",
        "churn_p95",
        "hr_std",
        "hr_min",
        "hr_max",
        "cross_moves_interval_total",
        "same_refresh_interval_total",
        "evictions_total",
        "promotions_total",
        "cross_level_promotions_total",
        "same_level_refresh_total",
        "interval_count",
        "run_summary_path",
        "interval_summary_path",
    ]

    out_dir = os.path.dirname(output_csv)
    if out_dir:
        os.makedirs(out_dir, exist_ok=True)

    ranked = sorted(
        rows,
        key=lambda r: (
            -r["get_hit_rate"],
            r["get_latency_avg_ms"],
            r["churn_mean"],
        ),
    )

    with open(output_csv, "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=fieldnames)
        w.writeheader()
        for r in ranked:
            w.writerow({k: r.get(k, "") for k in fieldnames})


def main():
    parser = argparse.ArgumentParser(description="Comprehensive Stage 2 analysis for LFU threshold tuning")
    parser.add_argument("--base-dir", required=True, help="Root workspace directory (e.g. /home/user/EthCaching)")
    parser.add_argument("--stage-dir", required=True, help="Stage 2 tuning runs directory (e.g. /home/user/EthCaching/tuning_runs/stage2)")
    parser.add_argument("--master-csv", default="", help="Optional explicit master results CSV path")
    parser.add_argument("--anchor", default="1|3|6|12|24", help="Anchor thresholds for one-knob sensitivity")
    parser.add_argument("--output-csv", default="", help="Optional path to write ranked analysis CSV")
    args = parser.parse_args()

    base_dir = os.path.abspath(args.base_dir)
    stage_dir = os.path.abspath(args.stage_dir)
    master_csv = os.path.abspath(find_master_csv(stage_dir, args.master_csv.strip()))

    rows = load_stage2_rows(base_dir, stage_dir, master_csv)
    if not rows:
        raise RuntimeError(f"no rows found in master csv: {master_csv}")

    print(f"Analyzed {len(rows)} candidates from: {master_csv}")
    print_rankings(rows)
    print_one_knob_sensitivity(rows, args.anchor)
    print_pareto(rows)
    print_churn_breakdown(rows)
    maybe_write_csv(rows, args.output_csv.strip())


if __name__ == "__main__":
    main()
