#!/usr/bin/env python3
"""Build frequency-of-frequencies CSV from co-reads raw trace lines.

Expected input line example:
key: <key1>-<n>;<key2>-<n>; Freq: 2; Blocks: 20531048;20544008
"""

from __future__ import annotations

import argparse
import csv
import re
from collections import Counter

FREQ_RE = re.compile(r"\bFreq:\s*(\d+)\b")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Read co-reads raw trace and output CSV of frequency -> key_pair_count(co-reads)."
        )
    )
    parser.add_argument("input", help="Path to raw co-reads trace file")
    parser.add_argument(
        "-o",
        "--output",
        default="co_reads_frequency_of_frequencies.csv",
        help="Output CSV file path",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    freq_of_freq = Counter()

    with open(args.input, "r", encoding="utf-8") as f:
        for line in f:
            match = FREQ_RE.search(line)
            if not match:
                continue
            freq = int(match.group(1))
            freq_of_freq[freq] += 1

    with open(args.output, "w", newline="", encoding="utf-8") as out:
        writer = csv.writer(out)
        writer.writerow(["frequency", "key_pair_count(co-reads)"])
        for freq in sorted(freq_of_freq):
            writer.writerow([freq, freq_of_freq[freq]])

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
