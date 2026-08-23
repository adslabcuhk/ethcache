#!/usr/bin/env python3
import csv
from collections import Counter

# Read CSV file
with open('correlation_log_20260221_235004.csv', 'r') as f:
    reader = csv.DictReader(f)
    block_sizes = []
    
    for row in reader:
        block_sizes.append(int(row['BlockCount']))

# Calculate statistics
total_blocks = len(block_sizes)
total_kv_pairs = sum(block_sizes)
size_distribution = Counter(block_sizes)

print("=" * 60)
print("Correlation Block Size Distribution Analysis")
print("=" * 60)
print(f"\nTotal correlated blocks: {total_blocks}")
print(f"Total KV pairs in correlated blocks: {total_kv_pairs}")
print(f"Average block size: {total_kv_pairs/total_blocks:.2f}")
print(f"Min block size: {min(block_sizes)}")
print(f"Max block size: {max(block_sizes)}")
print(f"Median block size: {sorted(block_sizes)[total_blocks//2]}")

print("\n" + "=" * 60)
print("Block Size Distribution (sorted by size):")
print("=" * 60)
print(f"{'Size':<10} {'Count':<10} {'Percentage':<12} {'Cumulative %'}")
print("-" * 60)

cumulative = 0
for size in sorted(size_distribution.keys()):
    count = size_distribution[size]
    percentage = (count / total_blocks) * 100
    cumulative += percentage
    print(f"{size:<10} {count:<10} {percentage:>10.2f}%   {cumulative:>10.2f}%")

print("\n" + "=" * 60)
print("Block Size Range Distribution:")
print("=" * 60)

ranges = [
    (1, 10, "1-10"),
    (11, 50, "11-50"),
    (51, 100, "51-100"),
    (101, 200, "101-200"),
    (201, 500, "201-500"),
    (501, 1000, "501-1000"),
]

print(f"{'Range':<15} {'Count':<10} {'Percentage':<12} {'KV Pairs':<12} {'KV %'}")
print("-" * 70)

for min_size, max_size, label in ranges:
    count = sum(1 for s in block_sizes if min_size <= s <= max_size)
    kv_count = sum(s for s in block_sizes if min_size <= s <= max_size)
    if count > 0:
        percentage = (count / total_blocks) * 100
        kv_percentage = (kv_count / total_kv_pairs) * 100
        print(f"{label:<15} {count:<10} {percentage:>10.2f}%   {kv_count:<12} {kv_percentage:>8.2f}%")
