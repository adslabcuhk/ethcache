package main

import (
	"bufio"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"

	"sim/ethcache"
)

const (
	defaultOldDBPath = "/mnt/ssd/block-20500000-backup/execution/data/geth/chaindata"
	// defaultNewDBPath = "/mnt/ssd/block-20500000-backup/execution/data/geth/chaindata-kvsopt"
	lfuMinPebbleCacheBytes int64 = 16 * 1024 * 1024 // 16 MiB
	lfuPebbleMemTables     int   = 2
)

type traceOperation struct {
	typeName string
	keyHex   string
	valueHex string
}

type pebbleKVGetStats struct {
	dbGetRequests              int64
	servedByBlockCacheRequests int64
	blockCacheMissRequests     int64
	blockCacheNoSignalRequests int64
	blockCacheHitEvents        int64
	blockCacheMissEvents       int64
	maxBlockCacheSizeBytes     int64
}

type pebbleCacheSizing struct {
	DatabaseCacheBytes          int64
	BlockCacheBytes             int64
	MemTableSizeBytes           int64
	MemTableStopWritesThreshold int
}

func main() {
	var (
		tracePath   = flag.String("trace", "", "path to trace log file")
		oldDBPath   = flag.String("olddb", defaultOldDBPath, "path to read-only source database")
		// newDBPath   = flag.String("newdb", defaultNewDBPath, "path to writable destination database")
		cacheSize   = flag.Int64("cache", 1<<30, "maximum cache size in bytes")
		thresholds  = flag.String("thresholds", "", "comma-separated frequency thresholds (e.g. 1,2,4,8)")
		metricsMode = flag.String("metricsMode", "summary", "metrics mode: summary or full")
		metricsPath = flag.String("metrics", "cache_metrics.csv", "per-operation metrics output path (used only when metricsMode=full)")
		runSummary  = flag.String("runSummary", "run_summary.csv", "run-level summary CSV output path")
		intervalOut = flag.String("intervalSummary", "interval_summary.csv", "interval summary CSV output path")
		intervalOps = flag.Int("intervalOps", 100000, "operations per interval summary row")
		startBlock  = flag.Int64("startBlock", -1, "The starting block number to process.")
		endBlock    = flag.Int64("endBlock", -1, "The ending block number to process.")
	)
	flag.Parse()

	if *tracePath == "" {
		log.Fatal("trace log path must be provided via -trace")
	}

	parsedThresholds, err := parseThresholdsFlag(*thresholds)
	if err != nil {
		log.Fatalf("parse thresholds: %v", err)
	}

	metrics, err := newMetricsRecorder(*metricsPath, *metricsMode)
	if err != nil {
		log.Fatalf("create metrics recorder: %v", err)
	}
	defer func() {
		if err := metrics.Close(); err != nil {
			log.Printf("close metrics recorder: %v", err)
		}
	}()

	intervalMetrics, err := newIntervalRecorder(*intervalOut, *intervalOps)
	if err != nil {
		log.Fatalf("create interval recorder: %v", err)
	}
	defer func() {
		if err := intervalMetrics.Close(); err != nil {
			log.Printf("close interval recorder: %v", err)
		}
	}()

	cacheEngine := ethcache.NewCache[string, []byte](normalizeByteCapacity(*cacheSize), parsedThresholds)
	runStartedAt := time.Now()
	pebbleCfg := computePebbleCacheSizing(*cacheSize)
	log.Printf("pebble config: total_cache_bytes=%d database_cache_bytes=%d block_cache_bytes=%d memtable_size_bytes=%d memtable_stop_writes_threshold=%d",
		*cacheSize, pebbleCfg.DatabaseCacheBytes, pebbleCfg.BlockCacheBytes, pebbleCfg.MemTableSizeBytes, pebbleCfg.MemTableStopWritesThreshold)

	oldDBCache := pebble.NewCache(pebbleCfg.BlockCacheBytes)
	defer oldDBCache.Unref()

	oldDB, err := pebble.Open(*oldDBPath, &pebble.Options{
		ReadOnly:                    false,
		Cache:                       oldDBCache,
		MemTableSize:                uint64(pebbleCfg.MemTableSizeBytes),
		MemTableStopWritesThreshold: pebbleCfg.MemTableStopWritesThreshold,
	})
	if err != nil {
		log.Fatalf("open old db: %v", err)
	}
	defer oldDB.Close()

	// if err := os.MkdirAll(*newDBPath, 0o755); err != nil {
	// 	log.Fatalf("prepare new db directory: %v", err)
	// }
	// newDB, err := pebble.Open(*newDBPath, &pebble.Options{})
	// if err != nil {
	// 	log.Fatalf("open new db: %v", err)
	// }
	// defer func() {
	// 	if err := newDB.Close(); err != nil {
	// 		log.Printf("close new db: %v", err)
	// 	}
	// }()

	traceFile, err := os.Open(*tracePath)
	if err != nil {
		log.Fatalf("open trace file: %v", err)
	}
	defer traceFile.Close()

	scanner := bufio.NewScanner(traceFile)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var (
		getOps     int
		getHits    int
		putOps     int
		lines      int
		failures   int
		processing = (*startBlock == -1) // Start processing immediately if no start block is set
		kvGetStats pebbleKVGetStats
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines++

		// if lines%10000 == 0 {
		// 	fmt.Printf("\rProcessed %d lines", lines)
		// }

		if strings.Contains(line, "Processing block (start)") {
			blockID, err := parseBlockID(line)
			log.Printf("Processing block (start) %d\n", blockID)
			if err == nil {
				if !processing && blockID >= *startBlock {
					processing = true
					log.Printf("Started processing at block %d", blockID)
				}
				if processing && *endBlock != -1 && blockID > *endBlock {
					log.Printf("Finished processing. Reached block %d which is after end block %d", blockID, *endBlock)
					break
				}
			}
		}

		if strings.Contains(line, "Processing block (end)") {
			blockID, err := parseBlockID(line)
			log.Printf("Processing block (end) %d\n", blockID)
			if err == nil {
				if processing && *endBlock != -1 && blockID == *endBlock {
					log.Printf("Finished processing at end of block %d", blockID)
					break
				}
			}
		}

		if !processing {
			continue
		}

		op, err := parseTraceLine(line)
		if err != nil {
			// This line is not a KV operation, but could be a block start/end marker.
			// We've already handled the logic for it, so we can just continue.
			continue
		}

		switch strings.ToLower(op.typeName) {
		case "get":
			getOps++
			start := time.Now()
			hit := replayGet(cacheEngine, oldDB, op.keyHex, &kvGetStats)
			latency := time.Since(start)
			metrics.RecordGet(op.keyHex, hit, latency)
			intervalMetrics.RecordGet(hit, latency)
			if hit {
				getHits++
			}
			if intervalMetrics.ShouldFlush() {
				flushErr := intervalMetrics.Flush(metrics.HitRate(), cacheEngine.SnapshotStats())
				if flushErr != nil {
					log.Printf("flush interval summary: %v", flushErr)
				}
			}
		case "put":
			putOps++
			intervalMetrics.RecordPut()
			if err := replayPut(cacheEngine, oldDB, op.keyHex, op.valueHex); err != nil {
				failures++
				log.Printf("put error on line %d: %v", lines, err)
			}
			if intervalMetrics.ShouldFlush() {
				flushErr := intervalMetrics.Flush(metrics.HitRate(), cacheEngine.SnapshotStats())
				if flushErr != nil {
					log.Printf("flush interval summary: %v", flushErr)
				}
			}
		case "batchput":
			putOps++
			intervalMetrics.RecordPut()
			if err := replayPut(cacheEngine, oldDB, op.keyHex, op.valueHex); err != nil {
				failures++
				log.Printf("batchput error on line %d: %v", lines, err)
			}
			if intervalMetrics.ShouldFlush() {
				flushErr := intervalMetrics.Flush(metrics.HitRate(), cacheEngine.SnapshotStats())
				if flushErr != nil {
					log.Printf("flush interval summary: %v", flushErr)
				}
			}
		default:
			// log.Printf("unknown operation '%s' on line %d", op.typeName, lines)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("read trace: %v", err)
	}

	hitRate := metrics.HitRate()
	avgLatency := metrics.AverageLatencyUs()
	p95Latency := metrics.P95LatencyUs()
	if err := intervalMetrics.FlushRemainder(metrics.HitRate(), cacheEngine.SnapshotStats()); err != nil {
		log.Printf("flush remaining interval summary: %v", err)
	}

	finalStats := cacheEngine.SnapshotStats()
	if err := writeRunSummary(*runSummary, runSummaryRow{
		TimestampMS:               time.Now().UnixMilli(),
		DurationSec:               time.Since(runStartedAt).Seconds(),
		TracePath:                 *tracePath,
		StartBlock:                *startBlock,
		EndBlock:                  *endBlock,
		CacheBytes:                normalizeByteCapacity(*cacheSize),
		ThresholdsInput:           *thresholds,
		ThresholdsApplied:         finalStats.Thresholds,
		Levels:                    len(finalStats.Levels),
		Gets:                      getOps,
		Hits:                      getHits,
		HitRate:                   hitRate,
		AvgLatencyUS:              avgLatency,
		P95LatencyUS:              p95Latency,
		Puts:                      putOps,
		Failures:                  failures,
		PromotionsTotal:           finalStats.PromotionsTotal,
		CrossLevelPromotionsTotal: finalStats.CrossLevelPromotionsTotal,
		SameLevelRefreshTotal:     finalStats.SameLevelRefreshTotal,
		EvictionsTotal:            finalStats.EvictionsTotal,
		EvictionsByLevel:          finalStats.EvictionsByLevel,
		EvictedBytesByLevel:       finalStats.EvictedBytesByLevel,
		LevelEntries:              levelEntries(finalStats.Levels),
		LevelBytes:                levelBytes(finalStats.Levels),
		MetricsMode:               normalizeMetricsMode(*metricsMode),
		IntervalOps:               *intervalOps,
	}); err != nil {
		log.Printf("write run summary: %v", err)
	}
	logLFUCacheUsage(cacheEngine, finalStats)
	logPebbleBlockCacheStats(oldDB, &kvGetStats)
	logPebbleKVGetStats(oldDB, kvGetStats)
	log.Printf("replay complete: %d lines processed, %d GET ops (%d hits, %.2f%%), %d PUT/BATCHPUT ops, %d failures, avg latency %.3f us, p95 latency %.3f us", lines, getOps, getHits, hitRate*100, putOps, failures, avgLatency, p95Latency)

	// 2026/01/04 01:39:29 replay complete: 17833 lines processed, 9984 GET ops (1637 hits), 7758 PUT/BATCHPUT ops, 18 failures
	// 2026/01/04 11:11:20 replay complete: 17833 lines processed, 9984 GET ops (3568 hits), 7758 PUT/BATCHPUT ops, 18 failures
	// 2026/01/04 12:41:14 replay complete: 17833 lines processed, 9984 GET ops (3568 hits), 7758 PUT/BATCHPUT ops, 18 failures

	// lfu+lru
	//2026/02/08 11:54:34 replay complete: 17833 lines processed, 9984 GET ops (3568 hits, 35.74%), 7758 PUT/BATCHPUT ops, 18 failures, avg latency 0.035 ms

	// 4k cache
	// 2026/02/08 12:17:28 replay complete: 17833 lines processed, 9984 GET ops (3173 hits, 31.78%), 7758 PUT/BATCHPUT ops, 18 failures, avg latency 0.032 ms

	// lru 16k cache
	// 2026/02/08 12:27:20 replay complete: 17833 lines processed, 9984 GET ops (289 hits), 7758 PUT/BATCHPUT ops, 18 failures

	// lfu+lru 1G cache
	// 2026/02/09 12:20:30 replay complete: 1919367 lines processed, 985089 GET ops (627839 hits, 63.73%), 920789 PUT/BATCHPUT ops, 3507 failures, avg latency 0.027 ms

	// lru 1G cache
	// 2026/02/09 16:00:34 replay complete: 1919367 lines processed, 985089 GET ops (627813 hits, 63.73%), 920789 PUT/BATCHPUT ops, 3507 failures

}

func parseBlockID(line string) (int64, error) {
	idIndex := strings.Index(line, "ID:")
	if idIndex == -1 {
		return 0, fmt.Errorf("missing 'ID:' in line")
	}
	// Move past "ID: "
	idPart := line[idIndex+len("ID:"):]
	idStr := strings.Fields(idPart)[0]
	// Remove trailing comma if present
	idStr = strings.TrimRight(idStr, ",")
	return strconv.ParseInt(idStr, 10, 64)
}

func parseTraceLine(line string) (traceOperation, error) {
	idx := strings.Index(line, "OPType:")
	if idx == -1 {
		return traceOperation{}, fmt.Errorf("missing OPType: prefix")
	}
	rest := line[idx+len("OPType:"):]
	parts := strings.Split(rest, ",")
	if len(parts) < 2 {
		return traceOperation{}, fmt.Errorf("incomplete operation fields")
	}

	opType := strings.TrimSpace(parts[0])
	var keyHex, valueHex string
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "key:"):
			keyHex = strings.TrimSpace(strings.TrimPrefix(part, "key:"))
		case strings.HasPrefix(part, "value:"):
			valueHex = strings.TrimSpace(strings.TrimPrefix(part, "value:"))
		}
	}

	if keyHex == "" {
		return traceOperation{}, fmt.Errorf("missing key field")
	}

	return traceOperation{
		typeName: opType,
		keyHex:   strings.TrimSpace(keyHex),
		valueHex: strings.TrimSpace(valueHex),
	}, nil
}

func replayGet(cacheEngine *ethcache.Cache[string, []byte], oldDB *pebble.DB, keyHex string, stats *pebbleKVGetStats) bool {
	if keyHex == "" {
		return false
	}

	if _, ok := cacheEngine.Get(keyHex); ok {
		return true
	}

	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		log.Printf("invalid hex key %q: %v", keyHex, err)
		return false
	}

	// if value, err := readFromDB(newDB, keyBytes); err == nil {
	// 	cacheEngine.Put(keyHex, value)
	// 	return false
	// } else if !errors.Is(err, pebble.ErrNotFound) {
	// 	log.Printf("read new db: %v", err)
	// 	return false
	// }

	var beforeHits, beforeMisses int64
	if stats != nil && oldDB != nil {
		m := oldDB.Metrics().BlockCache
		beforeHits = m.Hits
		beforeMisses = m.Misses
		if m.Size > stats.maxBlockCacheSizeBytes {
			stats.maxBlockCacheSizeBytes = m.Size
		}
		stats.dbGetRequests++
	}

	value, err := readFromDB(oldDB, keyBytes)
	if err != nil {
		if !errors.Is(err, pebble.ErrNotFound) {
			log.Printf("read old db: %v", err)
		}
		if stats != nil && oldDB != nil {
			after := oldDB.Metrics().BlockCache
			dh := after.Hits - beforeHits
			dm := after.Misses - beforeMisses
			if after.Size > stats.maxBlockCacheSizeBytes {
				stats.maxBlockCacheSizeBytes = after.Size
			}
			if dh > 0 {
				stats.blockCacheHitEvents += dh
			}
			if dm > 0 {
				stats.blockCacheMissEvents += dm
				stats.blockCacheMissRequests++
			} else if dh > 0 {
				stats.servedByBlockCacheRequests++
			} else {
				stats.blockCacheNoSignalRequests++
			}
		}
		return false
	}

	if stats != nil && oldDB != nil {
		after := oldDB.Metrics().BlockCache
		dh := after.Hits - beforeHits
		dm := after.Misses - beforeMisses
		if after.Size > stats.maxBlockCacheSizeBytes {
			stats.maxBlockCacheSizeBytes = after.Size
		}
		if dh > 0 {
			stats.blockCacheHitEvents += dh
		}
		if dm > 0 {
			stats.blockCacheMissEvents += dm
			stats.blockCacheMissRequests++
		} else if dh > 0 {
			stats.servedByBlockCacheRequests++
		} else {
			stats.blockCacheNoSignalRequests++
		}
	}

	cacheEngine.Put(keyHex, value)
	return false
}

func replayPut(cacheEngine *ethcache.Cache[string, []byte], oldDB *pebble.DB, keyHex, valueHex string) error {
	if keyHex == "" {
		return fmt.Errorf("empty key for put")
	}
	if valueHex == "" {
		return fmt.Errorf("empty value for put")
	}

	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return fmt.Errorf("decode key: %w", err)
	}
	valueBytes, err := hex.DecodeString(valueHex)
	if err != nil {
		return fmt.Errorf("decode value: %w", err)
	}

	if err := oldDB.Set(keyBytes, valueBytes, pebble.Sync); err != nil {
		return fmt.Errorf("write old db: %w", err)
	}

	cacheEngine.Put(keyHex, valueBytes)
	return nil
}

type metricsRecorder struct {
	mode         string
	file         *os.File
	writer       *csv.Writer
	totalGets    int
	hitGets      int
	totalLatency time.Duration
	latencyUs    []float64
}

func newMetricsRecorder(path string, mode string) (*metricsRecorder, error) {
	mode = normalizeMetricsMode(mode)
	recorder := &metricsRecorder{mode: mode}
	if mode != "full" {
		return recorder, nil
	}

	if path == "" {
		return nil, fmt.Errorf("metrics path must not be empty in full mode")
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create metrics directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create metrics file: %w", err)
	}

	writer := csv.NewWriter(file)
	header := []string{"timestamp_ms", "key_hex", "hit", "latency_us"}
	if err := writer.Write(header); err != nil {
		file.Close()
		return nil, fmt.Errorf("write metrics header: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		file.Close()
		return nil, fmt.Errorf("flush metrics header: %w", err)
	}

	recorder.file = file
	recorder.writer = writer
	return recorder, nil
}

func (r *metricsRecorder) RecordGet(key string, hit bool, latency time.Duration) {
	if r == nil {
		return
	}

	r.totalGets++
	if hit {
		r.hitGets++
	}
	r.totalLatency += latency
	r.latencyUs = append(r.latencyUs, float64(latency)/float64(time.Microsecond))

	if r.mode != "full" || r.writer == nil {
		return
	}

	record := []string{
		strconv.FormatInt(time.Now().UnixMilli(), 10),
		key,
		hitLabel(hit),
		fmt.Sprintf("%.6f", float64(latency)/float64(time.Microsecond)),
	}

	if err := r.writer.Write(record); err != nil {
		log.Printf("write metrics row: %v", err)
		return
	}
	r.writer.Flush()
	if err := r.writer.Error(); err != nil {
		log.Printf("flush metrics row: %v", err)
	}
}

func (r *metricsRecorder) HitRate() float64 {
	if r == nil || r.totalGets == 0 {
		return 0
	}
	return float64(r.hitGets) / float64(r.totalGets)
}

func (r *metricsRecorder) AverageLatencyUs() float64 {
	if r == nil || r.totalGets == 0 {
		return 0
	}
	return float64(r.totalLatency) / float64(time.Microsecond) / float64(r.totalGets)
}

func (r *metricsRecorder) P95LatencyUs() float64 {
	if r == nil || len(r.latencyUs) == 0 {
		return 0
	}
	vals := append([]float64(nil), r.latencyUs...)
	sort.Float64s(vals)
	idx := int(math.Ceil(0.95*float64(len(vals)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx]
}

func (r *metricsRecorder) Close() error {
	if r == nil {
		return nil
	}
	if r.writer == nil || r.file == nil {
		return nil
	}
	r.writer.Flush()
	if err := r.writer.Error(); err != nil {
		_ = r.file.Close()
		return err
	}
	return r.file.Close()
}

func hitLabel(hit bool) string {
	if hit {
		return "hit"
	}
	return "miss"
}

type runSummaryRow struct {
	TimestampMS               int64
	DurationSec               float64
	TracePath                 string
	StartBlock                int64
	EndBlock                  int64
	CacheBytes                int64
	ThresholdsInput           string
	ThresholdsApplied         []int
	Levels                    int
	Gets                      int
	Hits                      int
	HitRate                   float64
	AvgLatencyUS              float64
	P95LatencyUS              float64
	Puts                      int
	Failures                  int
	PromotionsTotal           uint64
	CrossLevelPromotionsTotal uint64
	SameLevelRefreshTotal     uint64
	EvictionsTotal            uint64
	EvictionsByLevel          []uint64
	EvictedBytesByLevel       []int64
	LevelEntries              []int
	LevelBytes                []int64
	MetricsMode               string
	IntervalOps               int
}

func writeRunSummary(path string, row runSummaryRow) error {
	if path == "" {
		return fmt.Errorf("run summary path must not be empty")
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create run summary directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create run summary file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	header := []string{
		"timestamp_ms", "duration_sec", "trace", "start_block", "end_block", "cache_bytes",
		"thresholds_input", "thresholds_normalized", "levels",
		"get_total", "get_hits", "get_hit_rate", "get_latency_avg_us", "get_latency_p95_us",
		"put_total", "failures",
		"promotions_total", "cross_level_promotions_total", "same_level_refresh_total", "evictions_total", "evictions_by_level", "evicted_bytes_by_level",
		"level_entries_end", "level_bytes_end", "metrics_mode", "interval_ops",
	}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("write run summary header: %w", err)
	}
	record := []string{
		strconv.FormatInt(row.TimestampMS, 10),
		fmt.Sprintf("%.3f", row.DurationSec),
		row.TracePath,
		strconv.FormatInt(row.StartBlock, 10),
		strconv.FormatInt(row.EndBlock, 10),
		strconv.FormatInt(row.CacheBytes, 10),
		row.ThresholdsInput,
		joinInts(row.ThresholdsApplied),
		strconv.Itoa(row.Levels),
		strconv.Itoa(row.Gets),
		strconv.Itoa(row.Hits),
		fmt.Sprintf("%.6f", row.HitRate),
		fmt.Sprintf("%.6f", row.AvgLatencyUS),
		fmt.Sprintf("%.6f", row.P95LatencyUS),
		strconv.Itoa(row.Puts),
		strconv.Itoa(row.Failures),
		strconv.FormatUint(row.PromotionsTotal, 10),
		strconv.FormatUint(row.CrossLevelPromotionsTotal, 10),
		strconv.FormatUint(row.SameLevelRefreshTotal, 10),
		strconv.FormatUint(row.EvictionsTotal, 10),
		joinUint64s(row.EvictionsByLevel),
		joinInt64s(row.EvictedBytesByLevel),
		joinInts(row.LevelEntries),
		joinInt64s(row.LevelBytes),
		row.MetricsMode,
		strconv.Itoa(row.IntervalOps),
	}
	if err := writer.Write(record); err != nil {
		return fmt.Errorf("write run summary row: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush run summary: %w", err)
	}
	return nil
}

type intervalRecorder struct {
	file                     *os.File
	writer                   *csv.Writer
	intervalOps              int
	ops                      int
	intervalGets             int
	intervalHits             int
	intervalPuts             int
	intervalLatency          time.Duration
	latencyUs                []float64
	lastCrossLevelPromotions uint64
	lastPromotions           uint64
	lastEvictions            uint64
}

func newIntervalRecorder(path string, intervalOps int) (*intervalRecorder, error) {
	if intervalOps <= 0 {
		intervalOps = 100000
	}
	if path == "" {
		return nil, fmt.Errorf("interval summary path must not be empty")
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create interval summary directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create interval summary file: %w", err)
	}
	writer := csv.NewWriter(file)
	header := []string{
		"timestamp_ms", "ops_interval", "get_interval", "hits_interval", "interval_hit_rate", "cumulative_hit_rate",
		"get_latency_avg_us", "get_latency_p95_us", "put_interval",
		"promotions_interval", "cross_level_promotions_interval", "same_level_refresh_interval", "evictions_interval", "promotions_total", "cross_level_promotions_total", "same_level_refresh_total", "evictions_total", "evictions_by_level", "evicted_bytes_by_level",
		"level_entries", "level_bytes", "thresholds", "churn_index",
	}
	if err := writer.Write(header); err != nil {
		file.Close()
		return nil, fmt.Errorf("write interval summary header: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		file.Close()
		return nil, fmt.Errorf("flush interval summary header: %w", err)
	}

	return &intervalRecorder{
		file:        file,
		writer:      writer,
		intervalOps: intervalOps,
	}, nil
}

func (r *intervalRecorder) RecordGet(hit bool, latency time.Duration) {
	if r == nil {
		return
	}
	r.ops++
	r.intervalGets++
	if hit {
		r.intervalHits++
	}
	r.intervalLatency += latency
	r.latencyUs = append(r.latencyUs, float64(latency)/float64(time.Microsecond))
}

func (r *intervalRecorder) RecordPut() {
	if r == nil {
		return
	}
	r.ops++
	r.intervalPuts++
}

func (r *intervalRecorder) ShouldFlush() bool {
	if r == nil || r.intervalOps <= 0 {
		return false
	}
	return r.ops >= r.intervalOps
}

func (r *intervalRecorder) Flush(cumulativeHitRate float64, stats ethcache.StatsSnapshot) error {
	if r == nil {
		return nil
	}
	if !r.ShouldFlush() {
		return nil
	}
	return r.flush(cumulativeHitRate, stats)
}

func (r *intervalRecorder) FlushRemainder(cumulativeHitRate float64, stats ethcache.StatsSnapshot) error {
	if r == nil || r.ops == 0 {
		return nil
	}
	return r.flush(cumulativeHitRate, stats)
}

func (r *intervalRecorder) flush(cumulativeHitRate float64, stats ethcache.StatsSnapshot) error {
	if r.writer == nil {
		return nil
	}
	avg := 0.0
	if r.intervalGets > 0 {
		avg = float64(r.intervalLatency) / float64(time.Microsecond) / float64(r.intervalGets)
	}
	intervalHit := 0.0
	if r.intervalGets > 0 {
		intervalHit = float64(r.intervalHits) / float64(r.intervalGets)
	}
	p95 := percentile95(r.latencyUs)
	promotionsDelta := uint64(0)
	crossLevelPromotionsDelta := uint64(0)
	sameLevelRefreshDelta := uint64(0)
	evictionsDelta := uint64(0)
	if stats.PromotionsTotal >= r.lastPromotions {
		promotionsDelta = stats.PromotionsTotal - r.lastPromotions
	}
	if stats.CrossLevelPromotionsTotal >= r.lastCrossLevelPromotions {
		crossLevelPromotionsDelta = stats.CrossLevelPromotionsTotal - r.lastCrossLevelPromotions
	}
	if promotionsDelta >= crossLevelPromotionsDelta {
		sameLevelRefreshDelta = promotionsDelta - crossLevelPromotionsDelta
	}
	if stats.EvictionsTotal >= r.lastEvictions {
		evictionsDelta = stats.EvictionsTotal - r.lastEvictions
	}
	churn := 0.0
	if r.intervalGets > 0 {
		churn = float64(crossLevelPromotionsDelta+evictionsDelta) / float64(r.intervalGets)
	}

	record := []string{
		strconv.FormatInt(time.Now().UnixMilli(), 10),
		strconv.Itoa(r.ops),
		strconv.Itoa(r.intervalGets),
		strconv.Itoa(r.intervalHits),
		fmt.Sprintf("%.6f", intervalHit),
		fmt.Sprintf("%.6f", cumulativeHitRate),
		fmt.Sprintf("%.6f", avg),
		fmt.Sprintf("%.6f", p95),
		strconv.Itoa(r.intervalPuts),
		strconv.FormatUint(promotionsDelta, 10),
		strconv.FormatUint(crossLevelPromotionsDelta, 10),
		strconv.FormatUint(sameLevelRefreshDelta, 10),
		strconv.FormatUint(evictionsDelta, 10),
		strconv.FormatUint(stats.PromotionsTotal, 10),
		strconv.FormatUint(stats.CrossLevelPromotionsTotal, 10),
		strconv.FormatUint(stats.SameLevelRefreshTotal, 10),
		strconv.FormatUint(stats.EvictionsTotal, 10),
		joinUint64s(stats.EvictionsByLevel),
		joinInt64s(stats.EvictedBytesByLevel),
		joinInts(levelEntries(stats.Levels)),
		joinInt64s(levelBytes(stats.Levels)),
		joinInts(stats.Thresholds),
		fmt.Sprintf("%.6f", churn),
	}
	if err := r.writer.Write(record); err != nil {
		return fmt.Errorf("write interval summary row: %w", err)
	}
	r.writer.Flush()
	if err := r.writer.Error(); err != nil {
		return fmt.Errorf("flush interval summary row: %w", err)
	}

	r.ops = 0
	r.intervalGets = 0
	r.intervalHits = 0
	r.intervalPuts = 0
	r.intervalLatency = 0
	r.latencyUs = r.latencyUs[:0]
	r.lastCrossLevelPromotions = stats.CrossLevelPromotionsTotal
	r.lastPromotions = stats.PromotionsTotal
	r.lastEvictions = stats.EvictionsTotal
	return nil
}

func (r *intervalRecorder) Close() error {
	if r == nil {
		return nil
	}
	if r.writer != nil {
		r.writer.Flush()
		if err := r.writer.Error(); err != nil {
			_ = r.file.Close()
			return err
		}
	}
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

func parseThresholdsFlag(raw string) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid threshold %q: %w", p, err)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func normalizeMetricsMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "full" {
		return "full"
	}
	return "summary"
}

func percentile95(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	idx := int(math.Ceil(0.95*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func joinInts(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, "|")
}

func joinInt64s(values []int64) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(parts, "|")
}

func joinUint64s(values []uint64) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.FormatUint(v, 10)
	}
	return strings.Join(parts, "|")
}

func levelEntries(levels []ethcache.LevelStats) []int {
	out := make([]int, len(levels))
	for i, lvl := range levels {
		out[i] = lvl.Entries
	}
	return out
}

func levelBytes(levels []ethcache.LevelStats) []int64 {
	out := make([]int64, len(levels))
	for i, lvl := range levels {
		out[i] = lvl.Bytes
	}
	return out
}

func logLFUCacheUsage(cacheEngine *ethcache.Cache[string, []byte], stats ethcache.StatsSnapshot) {
	if cacheEngine == nil {
		return
	}
	capacity := cacheEngine.ByteCapacity()
	used := cacheEngine.BytesUsed()
	utilization := 0.0
	if capacity > 0 {
		utilization = 100 * float64(used) / float64(capacity)
	}
	log.Printf("lfu cache bytes: used_bytes=%d capacity_bytes=%d utilization=%.2f%%", used, capacity, utilization)
	for _, level := range stats.Levels {
		log.Printf("lfu cache level bytes: level=%d freq_range=[%d,%d] entries=%d bytes=%d", level.LevelID, level.MinFreq, level.MaxFreq, level.Entries, level.Bytes)
	}
}

func logPebbleBlockCacheStats(db *pebble.DB, stats *pebbleKVGetStats) {
	if db == nil {
		return
	}
	m := db.Metrics()
	hits := m.BlockCache.Hits
	misses := m.BlockCache.Misses
	maxSize := m.BlockCache.Size
	if stats != nil && stats.maxBlockCacheSizeBytes > maxSize {
		maxSize = stats.maxBlockCacheSizeBytes
	}
	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = 100 * float64(hits) / float64(total)
	}
	log.Printf("pebble block cache: hits=%d misses=%d size_bytes=%d max_size_bytes=%d hit_rate=%.2f%%", hits, misses, m.BlockCache.Size, maxSize, hitRate)
}

func logPebbleKVGetStats(db *pebble.DB, stats pebbleKVGetStats) {
	servedRate := 0.0
	if stats.dbGetRequests > 0 {
		servedRate = 100 * float64(stats.servedByBlockCacheRequests) / float64(stats.dbGetRequests)
	}
	rawHits, rawMisses, rawSize := int64(0), int64(0), int64(0)
	if db != nil {
		m := db.Metrics().BlockCache
		rawHits = m.Hits
		rawMisses = m.Misses
		rawSize = m.Size
	}
	log.Printf("pebble kv-get block-cache: db_get_requests=%d served_requests=%d miss_requests=%d no_signal_requests=%d served_rate=%.2f%% hit_events=%d miss_events=%d raw_hits=%d raw_misses=%d raw_size_bytes=%d max_size_bytes=%d",
		stats.dbGetRequests,
		stats.servedByBlockCacheRequests,
		stats.blockCacheMissRequests,
		stats.blockCacheNoSignalRequests,
		servedRate,
		stats.blockCacheHitEvents,
		stats.blockCacheMissEvents,
		rawHits,
		rawMisses,
		rawSize,
		stats.maxBlockCacheSizeBytes,
	)
}

func normalizeByteCapacity(v int64) int64 {
	if v <= 0 {
		return 0
	}
	return v
}

func computePebbleCacheSizing(totalCacheBytes int64) pebbleCacheSizing {
	databaseCache := int64(268435456) // 256 MiB
	// databaseCache := int64(536870912) // 512 MiB
	// databaseCache := int64(402653184) // 384 MiB
	if databaseCache < lfuMinPebbleCacheBytes {
		databaseCache = lfuMinPebbleCacheBytes
	}
	// databaseCache := totalCacheBytes / 2
	// totalCacheBytes = 1073741824 // 1 GiB
	memTableSize := databaseCache / 2 / int64(lfuPebbleMemTables)
	if memTableSize < 1 {
		memTableSize = 1
	}
	return pebbleCacheSizing{
		DatabaseCacheBytes:          databaseCache,
		BlockCacheBytes:             databaseCache,
		MemTableSizeBytes:           memTableSize,
		MemTableStopWritesThreshold: lfuPebbleMemTables,
	}
}

func readFromDB(db *pebble.DB, key []byte) ([]byte, error) {
	value, closer, err := db.Get(key)
	if err != nil {
		return nil, err
	}
	defer closer.Close()

	buf := make([]byte, len(value))
	copy(buf, value)
	return buf, nil
}
