#!/usr/bin/env python3
import argparse
import csv
from collections import Counter
import statistics
import sys

# Increase CSV field size limit
csv.field_size_limit(sys.maxsize)

parser = argparse.ArgumentParser(description="Analyze block correlation log CSV")
parser.add_argument("--input", required=True, help="Path to the correlation log CSV file")
args = parser.parse_args()

# Read the CSV file
data = []
with open(args.input, 'r') as f:
    reader = csv.DictReader(f)
    for row in reader:
        block_count = int(row['BlockCount'])
        data.append(block_count)

# Calculate statistics
total_blocks = len(data)
total_kv_pairs = sum(data)
min_block_size = min(data)
max_block_size = max(data)
avg_block_size = statistics.mean(data)
median_block_size = statistics.median(data)

# Distribution of unique block sizes
block_size_counter = Counter(data)
sorted_sizes = sorted(block_size_counter.items())

# Range distribution
ranges = [
    (1, 10),
    (11, 50),
    (51, 100),
    (101, 200),
    (201, 500),
    (501, 1000),
    (1001, float('inf'))
]

range_counts = {f"{r[0]}-{r[1] if r[1] != float('inf') else '1000+'}": 0 for r in ranges}
range_counts_friendly = {}

for block_size in data:
    for r_min, r_max in ranges:
        if r_min <= block_size <= r_max:
            key = f"{r_min}-{r_max if r_max != float('inf') else '1000+'}"
            range_counts[key] += 1
            break

# Print the report
print("=" * 80)
print("BLOCK SIZE DISTRIBUTION ANALYSIS REPORT")
print("=" * 80)
print()
print("OVERALL STATISTICS")
print("-" * 80)
print(f"Total Blocks:            {total_blocks:,}")
print(f"Total KV Pairs:          {total_kv_pairs:,}")
print(f"Minimum Block Size:      {min_block_size:,}")
print(f"Maximum Block Size:      {max_block_size:,}")
print(f"Average Block Size:      {avg_block_size:,.2f}")
print(f"Median Block Size:       {median_block_size:,.0f}")
print()

print("=" * 80)
print("UNIQUE BLOCK SIZE DISTRIBUTION")
print("-" * 80)
print(f"{'Block Size':<15} {'Count':<10} {'Percentage':<12}")
print("-" * 80)
for size, count in sorted_sizes:
    percentage = (count / total_blocks) * 100
    print(f"{size:<15} {count:<10} {percentage:>10.2f}%")

print()
print("=" * 80)
print("RANGE DISTRIBUTION")
print("-" * 80)
print(f"{'Range':<20} {'Count':<10} {'Percentage':<12}")
print("-" * 80)

# Define proper order for ranges
ordered_range_keys = ["1-10", "11-50", "51-100", "101-200", "201-500", "501-1000", "1001-1000+"]
for range_key in ordered_range_keys:
    count = range_counts[range_key]
    percentage = (count / total_blocks) * 100
    print(f"{range_key:<20} {count:<10} {percentage:>10.2f}%")

print()
print("=" * 80)
