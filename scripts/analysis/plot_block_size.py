#!/usr/bin/env python3
import matplotlib.pyplot as plt
import numpy as np
import re

# 读取analysis_output.txt文件
with open('analysis_output.txt', 'r') as f:
    content = f.read()

# 提取UNIQUE BLOCK SIZE DISTRIBUTION部分
start_idx = content.find('UNIQUE BLOCK SIZE DISTRIBUTION')
end_idx = content.find('RANGE DISTRIBUTION')
distribution_section = content[start_idx:end_idx]

# 解析数据
block_sizes = []
counts = []

lines = distribution_section.split('\n')[3:]  # 跳过标题行
for line in lines:
    if line.strip() and not line.startswith('-'):
        parts = line.split()
        if len(parts) >= 2 and parts[0].isdigit():
            try:
                size = int(parts[0])
                count = int(parts[1])
                block_sizes.append(size)
                counts.append(count)
            except ValueError:
                continue

print(f"Data parsed: {len(block_sizes)} unique block sizes")
print(f"Block size range: {min(block_sizes)} - {max(block_sizes)}")
print(f"Total blocks: {sum(counts)}")

# 创建多个图表
fig, axes = plt.subplots(2, 2, figsize=(16, 12))

# 1. 散点图 - 所有block size
ax = axes[0, 0]
ax.scatter(block_sizes, counts, alpha=0.6, s=20)
ax.set_xlabel('Block Size (KV pairs)', fontsize=12)
ax.set_ylabel('Frequency', fontsize=12)
ax.set_title('Block Size Distribution - Scatter Plot', fontsize=14, fontweight='bold')
ax.grid(True, alpha=0.3)

# 2. 直方图 - 限制在前500个block size
ax = axes[0, 1]
small_sizes = [(s, c) for s, c in zip(block_sizes, counts) if s <= 500]
if small_sizes:
    sizes_small, counts_small = zip(*small_sizes)
    ax.bar(range(len(sizes_small)), counts_small, width=1.0, alpha=0.7, edgecolor='black', linewidth=0.5)
    ax.set_xlabel('Block Size (KV pairs)', fontsize=12)
    ax.set_ylabel('Frequency', fontsize=12)
    ax.set_title('Block Size Distribution (Size ≤ 500) - Bar Chart', fontsize=14, fontweight='bold')
    # 每50个显示一个x轴标签
    tick_positions = range(0, len(sizes_small), 50)
    tick_labels = [sizes_small[i] for i in tick_positions]
    ax.set_xticks(tick_positions)
    ax.set_xticklabels(tick_labels, rotation=45)
    ax.grid(True, alpha=0.3, axis='y')

# 3. Range分布柱状图
ax = axes[1, 0]
ranges = ['1-10', '11-50', '51-100', '101-200', '201-500', '501-1000', '1001+']
range_counts = [400, 612, 299, 439, 598, 395, 237]
colors = ['#FF6B6B', '#4ECDC4', '#45B7D1', '#FFA07A', '#98D8C8', '#F7DC6F', '#BB8FCE']
bars = ax.bar(ranges, range_counts, color=colors, edgecolor='black', linewidth=1.5)
ax.set_ylabel('Number of Blocks', fontsize=12)
ax.set_title('Block Size Range Distribution', fontsize=14, fontweight='bold')
ax.grid(True, alpha=0.3, axis='y')

# 为每个柱子添加数值标签和百分比
total_blocks = sum(range_counts)
for bar, count in zip(bars, range_counts):
    height = bar.get_height()
    percentage = (count / total_blocks) * 100
    ax.text(bar.get_x() + bar.get_width()/2., height,
            f'{int(count)}\n({percentage:.1f}%)',
            ha='center', va='bottom', fontsize=10, fontweight='bold')

# 4. 对数刻度散点图 - 更好地显示长尾分布
ax = axes[1, 1]
ax.scatter(block_sizes, counts, alpha=0.6, s=20)
ax.set_xlabel('Block Size (KV pairs)', fontsize=12)
ax.set_ylabel('Frequency', fontsize=12)
ax.set_title('Block Size Distribution - Log Scale', fontsize=14, fontweight='bold')
ax.set_xscale('log')
ax.set_yscale('log')
ax.grid(True, alpha=0.3, which='both')

plt.tight_layout()
plt.savefig('block_size_distribution.png', dpi=300, bbox_inches='tight')
print("\n✓ Saved: block_size_distribution.png")

# 创建另一个更详细的图
fig, axes = plt.subplots(1, 2, figsize=(16, 6))

# 5. 前100个block size的详细视图
ax = axes[0]
top_100_sizes = [(s, c) for s, c in zip(block_sizes, counts) if s <= 100]
if top_100_sizes:
    sizes_100, counts_100 = zip(*top_100_sizes)
    colors_gradient = plt.cm.viridis(np.linspace(0, 1, len(sizes_100)))
    ax.bar(range(len(sizes_100)), counts_100, color=colors_gradient, edgecolor='black', linewidth=0.5)
    ax.set_xlabel('Block Size (KV pairs)', fontsize=12)
    ax.set_ylabel('Frequency', fontsize=12)
    ax.set_title('Block Size Distribution (Size ≤ 100) - Detailed View', fontsize=14, fontweight='bold')
    # 每5个显示一个x轴标签
    tick_positions = range(0, len(sizes_100), 5)
    tick_labels = [sizes_100[i] for i in tick_positions]
    ax.set_xticks(tick_positions)
    ax.set_xticklabels(tick_labels, rotation=45)
    ax.grid(True, alpha=0.3, axis='y')

# 6. 累积分布函数 (CDF)
ax = axes[1]
sorted_counts = sorted(counts, reverse=True)
cumsum = np.cumsum(sorted_counts)
cumsum_percent = (cumsum / cumsum[-1]) * 100
ax.plot(range(len(cumsum)), cumsum_percent, linewidth=2, color='#2E86AB', marker='o', markersize=3, markevery=50)
ax.axhline(y=80, color='r', linestyle='--', linewidth=2, label='80% threshold')
ax.axhline(y=50, color='orange', linestyle='--', linewidth=2, label='50% threshold')
ax.fill_between(range(len(cumsum)), cumsum_percent, alpha=0.3, color='#2E86AB')
ax.set_xlabel('Block Size Rank (sorted)', fontsize=12)
ax.set_ylabel('Cumulative Percentage (%)', fontsize=12)
ax.set_title('Cumulative Distribution Function (CDF)', fontsize=14, fontweight='bold')
ax.legend(fontsize=10)
ax.grid(True, alpha=0.3)

plt.tight_layout()
plt.savefig('block_size_detailed.png', dpi=300, bbox_inches='tight')
print("✓ Saved: block_size_detailed.png")

# 统计信息
print("\n" + "="*60)
print("BLOCK SIZE DISTRIBUTION STATISTICS")
print("="*60)
print(f"Total unique block sizes:    {len(block_sizes)}")
print(f"Total blocks:                {sum(counts):,}")
print(f"Block size range:            {min(block_sizes)} - {max(block_sizes)}")
avg_size = sum(s * c for s, c in zip(block_sizes, counts)) / sum(counts)
print(f"Average block size:          {avg_size:.2f}")
median_vals = [s for s, c in zip(block_sizes, counts) for _ in range(c)]
print(f"Median block size:           {np.median(median_vals):.2f}")
print(f"Mode (most common):          {max(zip(counts, block_sizes))[1]} blocks (appears {max(counts)} times)")

# 分析范围
print("\nBLOCK SIZE RANGE BREAKDOWN:")
print("-" * 60)
ranges_def = [(1, 10), (11, 50), (51, 100), (101, 200), (201, 500), (501, 1000), (1001, 10000)]
for start, end in ranges_def:
    count = sum(c for s, c in zip(block_sizes, counts) if start <= s <= end)
    pct = (count / sum(counts)) * 100
    print(f"{start:5d} - {end:5d}: {count:6d} blocks ({pct:6.2f}%)")

print("="*60)
