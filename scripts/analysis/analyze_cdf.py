#!/usr/bin/env python3
import csv
import numpy as np

# 设置CSV字段大小限制
csv.field_size_limit(10000000)

# 读取CSV文件
with open('correlation_log_20260311_001129.csv', 'r') as f:
    reader = csv.DictReader(f)
    block_sizes_expanded = []
    
    for row in reader:
        block_count = int(row['BlockCount'])
        block_sizes_expanded.append(block_count)

# 排序block sizes
sorted_sizes = sorted(block_sizes_expanded)
total = len(sorted_sizes)

# 计算百分位数
p50_idx = int(total * 0.50)
p80_idx = int(total * 0.80)
p95_idx = int(total * 0.95)
p99_idx = int(total * 0.99)

print("="*70)
print("CDF 分析 - Block Size Percentiles")
print("="*70)
print(f"\n总blocks数: {total:,}")
print(f"Block Size范围: {min(sorted_sizes)} - {max(sorted_sizes)}\n")

print(f"50% (中位数):    {sorted_sizes[p50_idx]:>6} 个KV对    (索引: {p50_idx})")
print(f"80% (P80):      {sorted_sizes[p80_idx]:>6} 个KV对    (索引: {p80_idx})")
print(f"95% (P95):      {sorted_sizes[p95_idx]:>6} 个KV对    (索引: {p95_idx})")
print(f"99% (P99):      {sorted_sizes[p99_idx]:>6} 个KV对    (索引: {p99_idx})")

print("\n" + "="*70)
print("详细统计")
print("="*70)

# 使用numpy计算更精确的百分位数
p50 = np.percentile(sorted_sizes, 50)
p80 = np.percentile(sorted_sizes, 80)
p95 = np.percentile(sorted_sizes, 95)
p99 = np.percentile(sorted_sizes, 99)

print(f"\n50% (中位数):    {p50:.2f} 个KV对")
print(f"80% (P80):      {p80:.2f} 个KV对")
print(f"95% (P95):      {p95:.2f} 个KV对")
print(f"99% (P99):      {p99:.2f} 个KV对")

print(f"\n平均值:         {np.mean(sorted_sizes):.2f} 个KV对")
print(f"标准差:         {np.std(sorted_sizes):.2f}")
print(f"最小值:         {np.min(sorted_sizes)} 个KV对")
print(f"最大值:         {np.max(sorted_sizes)} 个KV对")

print("\n" + "="*70)
print("CDF 完整表")
print("="*70)
percentiles = [1, 5, 10, 25, 50, 75, 90, 95, 99]
print(f"\n{'百分位数':<12} {'Block Size':<15} {'说明':<30}")
print("-"*70)
for p in percentiles:
    value = np.percentile(sorted_sizes, p)
    idx = int(total * (p/100))
    count = sorted_sizes[idx]
    
    if p == 50:
        desc = "中位数 (Median)"
    elif p == 80:
        desc = "80% block size以下"
    elif p == 90:
        desc = "90% block size以下"
    else:
        desc = f"P{p}"
    
    print(f"{p}%{'':<9} {value:>6.2f}{'':<9} {count:>6} {desc}")

print("\n" + "="*70)
