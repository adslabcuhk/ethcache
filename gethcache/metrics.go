package gethcache

import (
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// CacheOperation captures whether an operation was a GET or PUT.
type CacheOperation string

const (
	OperationGet CacheOperation = "GET"
	OperationPut CacheOperation = "PUT"
)

type operationRecord struct {
	Timestamp time.Time
	Operation CacheOperation
	Class     CacheClass
	Latency   time.Duration
	Hit       bool
	HitRatio  float64
}

// ClassMetrics tracks per-class aggregate statistics.
type ClassMetrics struct {
	Hits            int64
	Gets            int64
	Puts            int64
	TotalGetLatency time.Duration
	TotalPutLatency time.Duration
}

// MetricsSnapshot presents aggregate statistics for external reporting.
type MetricsSnapshot struct {
	Classes map[CacheClass]ClassSnapshot
}

// ClassSnapshot aggregates stats for a cache class.
type ClassSnapshot struct {
	Hits          int64
	Gets          int64
	Puts          int64
	HitRatio      float64
	AvgGetLatency float64
	AvgPutLatency float64
}

// MetricsCollector centralizes latency and hit tracking.
type MetricsCollector struct {
	mu         sync.Mutex
	operations []operationRecord
	classStats map[CacheClass]*ClassMetrics
}

// NewMetricsCollector creates a ready-to-use metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		operations: make([]operationRecord, 0, 128),
		classStats: make(map[CacheClass]*ClassMetrics),
	}
}

// RecordGet records a GET operation for a cache class.
func (mc *MetricsCollector) RecordGet(class CacheClass, latency time.Duration, hit bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	stats := mc.ensureStats(class)
	stats.Gets++
	stats.TotalGetLatency += latency
	if hit {
		stats.Hits++
	}
	mc.operations = append(mc.operations, operationRecord{
		Timestamp: time.Now(),
		Operation: OperationGet,
		Class:     class,
		Latency:   latency,
		Hit:       hit,
		HitRatio:  computeHitRatio(stats.Hits, stats.Gets),
	})
}

// RecordPut records a PUT operation for a cache class.
func (mc *MetricsCollector) RecordPut(class CacheClass, latency time.Duration) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	stats := mc.ensureStats(class)
	stats.Puts++
	stats.TotalPutLatency += latency
	mc.operations = append(mc.operations, operationRecord{
		Timestamp: time.Now(),
		Operation: OperationPut,
		Class:     class,
		Latency:   latency,
		Hit:       false,
		HitRatio:  computeHitRatio(stats.Hits, stats.Gets),
	})
}

// Snapshot creates an immutable view of the current aggregated metrics.
func (mc *MetricsCollector) Snapshot() MetricsSnapshot {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	snapshot := MetricsSnapshot{
		Classes: make(map[CacheClass]ClassSnapshot, len(mc.classStats)),
	}
	for class, stats := range mc.classStats {
		snapshot.Classes[class] = ClassSnapshot{
			Hits:          stats.Hits,
			Gets:          stats.Gets,
			Puts:          stats.Puts,
			HitRatio:      computeHitRatio(stats.Hits, stats.Gets),
			AvgGetLatency: averageLatency(stats.TotalGetLatency, stats.Gets),
			AvgPutLatency: averageLatency(stats.TotalPutLatency, stats.Puts),
		}
	}
	return snapshot
}

// FlushCSV appends the collected operations to the provided CSV file path.
// A header row is written if the file did not previously exist. Once flushed,
// the buffered operation records are cleared.
func (mc *MetricsCollector) FlushCSV(path string) error {
	if path == "" {
		return errors.New("metrics file path must not be empty")
	}

	mc.mu.Lock()
	records := make([]operationRecord, len(mc.operations))
	copy(records, mc.operations)
	mc.operations = mc.operations[:0]
	mc.mu.Unlock()

	if len(records) == 0 {
		return nil
	}

	needHeader := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		needHeader = true
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open metrics file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if needHeader {
		if err := writer.Write([]string{"timestamp", "operation", "class", "latency_ms", "hit", "hit_ratio"}); err != nil {
			writer.Flush()
			return fmt.Errorf("write metrics header: %w", err)
		}
	}

	for _, record := range records {
		latencyMillis := fmt.Sprintf("%.3f", float64(record.Latency.Microseconds())/1000.0)
		hitStr := strconv.FormatBool(record.Hit)
		ratioStr := fmt.Sprintf("%.4f", record.HitRatio)
		row := []string{
			record.Timestamp.Format(time.RFC3339Nano),
			string(record.Operation),
			string(record.Class),
			latencyMillis,
			hitStr,
			ratioStr,
		}
		if err := writer.Write(row); err != nil {
			writer.Flush()
			return fmt.Errorf("write metrics row: %w", err)
		}
	}
	writer.Flush()
	return writer.Error()
}

func (mc *MetricsCollector) ensureStats(class CacheClass) *ClassMetrics {
	stats, ok := mc.classStats[class]
	if !ok {
		stats = &ClassMetrics{}
		mc.classStats[class] = stats
	}
	return stats
}

func computeHitRatio(hits, gets int64) float64 {
	if gets == 0 {
		return 0
	}
	return float64(hits) / float64(gets)
}

func averageLatency(total time.Duration, count int64) float64 {
	if count == 0 {
		return 0
	}
	return float64(total.Microseconds()) / float64(count) / 1000.0
}
