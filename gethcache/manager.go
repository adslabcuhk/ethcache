package gethcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const defaultMetricsFilename = "cache_metrics.csv"

const (
	trieCleanPercent  int   = 15
	// snapshotPercent   int   = 10
	snapshotPercent   int   = 0 // Disable snapshot cache for non-cache trace
	codeCacheBytes    int64 = 64 * 1024 * 1024
	headerCacheItems  int   = 512
	blockBodyItems    int   = 512
	receiptCacheItems int   = 32
	txLookupItems     int   = 1024
	minPebbleCacheMB  int64 = 16 * 1024 * 1024
	pebbleMemTables   int   = 2
)

// PebbleCacheSizing mirrors Geth's Pebble cache-related sizing split.
type PebbleCacheSizing struct {
	DatabaseCacheBytes          int64
	BlockCacheBytes             int64
	MemTableSizeBytes           int64
	MemTableStopWritesThreshold int
}

// ComputePebbleCacheSizing derives Geth-like Pebble settings from total cache bytes.
func ComputePebbleCacheSizing(totalCacheBytes int64) PebbleCacheSizing {
	databaseCache := totalCacheBytes / 2
	if databaseCache < minPebbleCacheMB {
		databaseCache = minPebbleCacheMB
	}
	memTableSize := databaseCache / 2 / int64(pebbleMemTables)
	if memTableSize < 1 {
		memTableSize = 1
	}
	return PebbleCacheSizing{
		DatabaseCacheBytes:          databaseCache,
		BlockCacheBytes:             databaseCache,
		MemTableSizeBytes:           memTableSize,
		MemTableStopWritesThreshold: pebbleMemTables,
	}
}

// CacheRuntimeSnapshot reports configured limits and observed runtime peaks.
type CacheRuntimeSnapshot struct {
	Classes map[CacheClass]CacheRuntimeClassStats
}

// CacheRuntimeClassStats summarizes one cache instance.
type CacheRuntimeClassStats struct {
	LimitKind       string
	CapacityBytes   int64
	CapacityEntries int
	CurrentBytes    int64
	CurrentEntries  int
	MaxBytes        int64
	MaxEntries      int
}

// CacheManager orchestrates the per-class LRU caches and metrics collection.
type CacheManager struct {
	totalCapacity int64
	caches        map[CacheClass]*perClassCache
	metrics       *MetricsCollector
	metricsPath   string
}

// NewCacheManager builds a cache manager with the configured total capacity.
// Metrics are flushed to metricsFile. When metricsFile is empty, a default file
// named cache_metrics.csv is placed in the current working directory.
func NewCacheManager(totalCapacity int64, metricsFile string, params ...int64) (*CacheManager, error) {
	if totalCapacity <= 0 {
		return nil, errors.New("total capacity must be positive")
	}
	divide := int64(1)
	multiply := int64(1)
	if len(params) > 0 {
		divide = params[0]
	}
	if len(params) > 1 {
		multiply = params[1]
	}
	if divide <= 0 {
		return nil, errors.New("divide must be positive")
	}
	if multiply <= 0 {
		return nil, errors.New("multiply must be positive")
	}
	metricsPath := metricsFile
	if metricsPath == "" {
		metricsPath = defaultMetricsFilename
	}
	metricsPath = filepath.Clean(metricsPath)

	caches := make(map[CacheClass]*perClassCache, len(classOrder))
	for _, class := range classOrder {
		cfg, err := classConfigFor(class, totalCapacity, divide, multiply)
		if err != nil {
			return nil, err
		}
		caches[class] = newPerClassCache(cfg)
	}

	return &CacheManager{
		totalCapacity: totalCapacity,
		caches:        caches,
		metrics:       NewMetricsCollector(),
		metricsPath:   metricsPath,
	}, nil
}

// TotalCapacity returns the overall byte capacity managed by the cache.
func (m *CacheManager) TotalCapacity() int64 {
	return m.totalCapacity
}

// MetricsPath returns the configured metrics file path.
func (m *CacheManager) MetricsPath() string {
	return m.metricsPath
}

// CacheForClass exposes the backing LRU cache for testing and diagnostics.
func (m *CacheManager) CacheForClass(class CacheClass) (*perClassCache, bool) {
	cache, ok := m.caches[class]
	return cache, ok
}

// CacheRuntimeStats returns per-cache runtime stats including max byte usage.
func (m *CacheManager) CacheRuntimeStats() CacheRuntimeSnapshot {
	classes := make(map[CacheClass]CacheRuntimeClassStats, len(m.caches))
	for class, cache := range m.caches {
		stats := cache.stats()
		limitKind := "bytes"
		if stats.LimitKind == cacheLimitEntries {
			limitKind = "entries"
		}
		classes[class] = CacheRuntimeClassStats{
			LimitKind:       limitKind,
			CapacityBytes:   stats.CapacityBytes,
			CapacityEntries: stats.CapacityEntries,
			CurrentBytes:    stats.CurrentBytes,
			CurrentEntries:  stats.CurrentEntries,
			MaxBytes:        stats.MaxBytes,
			MaxEntries:      stats.MaxEntries,
		}
	}
	return CacheRuntimeSnapshot{Classes: classes}
}

// Put inserts the key-value pair using the cache class derived from the key bytes.
func (m *CacheManager) Put(key, value []byte) {
	if len(key) == 0 {
		return
	}
	class := classifyBytes(key)
	hexKey := fmt.Sprintf("%x", key)
	m.putHex(class, hexKey, value)
}

// PutHex inserts the key-value pair when the caller already has the hex-encoded key.
func (m *CacheManager) PutHex(hexKey string, value []byte) {
	if hexKey == "" {
		return
	}
	class := classifyHex(hexKey)
	m.putHex(class, hexKey, value)
}

// Get retrieves a cached value based on the raw key bytes.
func (m *CacheManager) Get(key []byte) ([]byte, bool) {
	if len(key) == 0 {
		return nil, false
	}
	class := classifyBytes(key)
	hexKey := fmt.Sprintf("%x", key)
	return m.getHex(class, hexKey)
}

// GetHex retrieves a cached value for an already hex-encoded key.
func (m *CacheManager) GetHex(hexKey string) ([]byte, bool) {
	if hexKey == "" {
		return nil, false
	}
	class := classifyHex(hexKey)
	return m.getHex(class, hexKey)
}

// FlushMetrics writes buffered operation metrics to the configured CSV file.
func (m *CacheManager) FlushMetrics() error {
	// Ensure directory exists when a custom path includes folders.
	dir := filepath.Dir(m.metricsPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create metrics directory: %w", err)
		}
	}

	return m.metrics.FlushCSV(m.metricsPath)
}

// SnapshotMetrics returns the current aggregate metrics view.
func (m *CacheManager) SnapshotMetrics() MetricsSnapshot {
	return m.metrics.Snapshot()
}

func (m *CacheManager) putHex(class CacheClass, hexKey string, value []byte) {
	cache := m.caches[class]
	if cache == nil {
		return
	}
	start := time.Now()
	cache.put(hexKey, value)
	duration := time.Since(start)
	m.metrics.RecordPut(class, duration)
}

func (m *CacheManager) getHex(class CacheClass, hexKey string) ([]byte, bool) {
	cache := m.caches[class]
	if cache == nil {
		return nil, false
	}
	start := time.Now()
	value, ok := cache.get(hexKey)
	duration := time.Since(start)
	m.metrics.RecordGet(class, duration, ok)
	return value, ok
}

func classConfigFor(class CacheClass, totalCapacity int64, divide int64, multiply int64) (perClassCacheConfig, error) {
	switch class {
	case CacheClassTrieClean:
		capacity := (totalCapacity * int64(trieCleanPercent)) / 100
		if capacity <= 0 {
			capacity = 1
		}
		return perClassCacheConfig{limitKind: cacheLimitBytes, capacityBytes: capacity}, nil
	case CacheClassSnapshot:
		capacity := (totalCapacity * int64(snapshotPercent)) / 100
		if capacity <= 0 {
			capacity = 1
		}
		return perClassCacheConfig{limitKind: cacheLimitBytes, capacityBytes: capacity}, nil
	case CacheClassCode:
		capacity := (codeCacheBytes * multiply) / divide
		if capacity <= 0 {
			capacity = 1
		}
		return perClassCacheConfig{limitKind: cacheLimitBytes, capacityBytes: capacity}, nil
	case CacheClassHeader:
		items := (headerCacheItems * int(multiply)) / int(divide)
		if items <= 0 {
			items = 1
		}
		return perClassCacheConfig{limitKind: cacheLimitEntries, capacityEntries: items}, nil
	case CacheClassBlockBody:
		items := (blockBodyItems * int(multiply)) / int(divide)
		if items <= 0 {
			items = 1
		}
		return perClassCacheConfig{limitKind: cacheLimitEntries, capacityEntries: items}, nil
	case CacheClassReceipt:
		items := (receiptCacheItems * int(multiply)) / int(divide)
		if items <= 0 {
			items = 1
		}
		return perClassCacheConfig{limitKind: cacheLimitEntries, capacityEntries: items}, nil
	case CacheClassTxLookup:
		items := (txLookupItems * int(multiply)) / int(divide)
		if items <= 0 {
			items = 1
		}
		return perClassCacheConfig{limitKind: cacheLimitEntries, capacityEntries: items}, nil
	default:
		return perClassCacheConfig{}, fmt.Errorf("unsupported cache class: %s", class)
	}
}
