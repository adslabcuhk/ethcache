package main

import (
	"bufio"
	"encoding/csv"
	"encoding/hex"
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

	"sim/arc"
)

type arcTraceOperation struct {
	typeName  string
	keyHex    string
	valueHex  string
	valueSize int
}

func main() {
	var (
		tracePath   = flag.String("trace", "", "path to trace log file")
		cacheSize   = flag.Int64("cache", 1<<30, "maximum cache size in bytes")
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

	metrics, err := newArcMetricsRecorder(*metricsPath, *metricsMode)
	if err != nil {
		log.Fatalf("create metrics recorder: %v", err)
	}
	defer func() {
		if err := metrics.Close(); err != nil {
			log.Printf("close metrics recorder: %v", err)
		}
	}()

	intervalMetrics, err := newArcIntervalRecorder(*intervalOut, *intervalOps)
	if err != nil {
		log.Fatalf("create interval recorder: %v", err)
	}
	defer func() {
		if err := intervalMetrics.Close(); err != nil {
			log.Printf("close interval recorder: %v", err)
		}
	}()

	cacheEngine := arc.NewCache(normalizeArcByteCapacity(*cacheSize))
	runStartedAt := time.Now()

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
		negSizeOps int
		processing = (*startBlock == -1)
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines++

		if strings.Contains(line, "Processing block (start)") {
			blockID, err := parseArcBlockID(line)
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
			blockID, err := parseArcBlockID(line)
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

		op, err := parseArcTraceLine(line)
		if err != nil {
			continue
		}

		switch strings.ToLower(op.typeName) {
		case "get":
			getOps++
			if op.valueSize < 0 {
				negSizeOps++
				break
			}
			start := time.Now()
			hit := arcReplayGet(cacheEngine, op.keyHex, op.valueSize)
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
		case "put", "batchput":
			putOps++
			intervalMetrics.RecordPut()
			if err := arcReplayPut(cacheEngine, op.keyHex, op.valueHex); err != nil {
				failures++
				log.Printf("%s error on line %d: %v", strings.ToLower(op.typeName), lines, err)
			}
			if intervalMetrics.ShouldFlush() {
				flushErr := intervalMetrics.Flush(metrics.HitRate(), cacheEngine.SnapshotStats())
				if flushErr != nil {
					log.Printf("flush interval summary: %v", flushErr)
				}
			}
		default:
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
	if err := writeArcRunSummary(*runSummary, arcRunSummaryRow{
		TimestampMS:   time.Now().UnixMilli(),
		DurationSec:   time.Since(runStartedAt).Seconds(),
		TracePath:     *tracePath,
		StartBlock:    *startBlock,
		EndBlock:      *endBlock,
		CacheBytes:    normalizeArcByteCapacity(*cacheSize),
		Gets:          getOps,
		Hits:          getHits,
		HitRate:       hitRate,
		AvgLatencyUS:  avgLatency,
		P95LatencyUS:  p95Latency,
		Puts:          putOps,
		Failures:      failures,
		Evictions:     finalStats.Evictions,
		B1Hits:        finalStats.B1Hits,
		B2Hits:        finalStats.B2Hits,
		TargetPBytes:  finalStats.TargetPBytes,
		TotalBytes:    finalStats.TotalBytes,
		T1Bytes:       finalStats.T1Bytes,
		T2Bytes:       finalStats.T2Bytes,
		B1Bytes:       finalStats.B1Bytes,
		B2Bytes:       finalStats.B2Bytes,
		T1Entries:     finalStats.T1Entries,
		T2Entries:     finalStats.T2Entries,
		B1Entries:     finalStats.B1Entries,
		B2Entries:     finalStats.B2Entries,
		MetricsMode:   normalizeArcMetricsMode(*metricsMode),
		IntervalOps:   *intervalOps,
		CapacityBytes: finalStats.CapacityBytes,
	}); err != nil {
		log.Printf("write run summary: %v", err)
	}
	log.Printf("arc cache bytes: total=%d capacity=%d p=%d t1=%d t2=%d b1=%d b2=%d", finalStats.TotalBytes, finalStats.CapacityBytes, finalStats.TargetPBytes, finalStats.T1Bytes, finalStats.T2Bytes, finalStats.B1Bytes, finalStats.B2Bytes)
	log.Printf("replay complete: %d lines processed, %d GET ops (%d hits, %.2f%%), %d PUT/BATCHPUT ops, valuesize<0 events %d, %d failures, avg latency %.3f us, p95 latency %.3f us", lines, getOps, getHits, hitRate*100, putOps, negSizeOps, failures, avgLatency, p95Latency)
}

func parseArcBlockID(line string) (int64, error) {
	idIndex := strings.Index(line, "ID:")
	if idIndex == -1 {
		return 0, fmt.Errorf("missing 'ID:' in line")
	}
	idPart := line[idIndex+len("ID:"):]
	idStr := strings.Fields(idPart)[0]
	idStr = strings.TrimRight(idStr, ",")
	return strconv.ParseInt(idStr, 10, 64)
}

func parseArcTraceLine(line string) (arcTraceOperation, error) {
	idx := strings.Index(line, "OPType:")
	if idx == -1 {
		return arcTraceOperation{}, fmt.Errorf("missing OPType: prefix")
	}
	rest := line[idx+len("OPType:"):]
	parts := strings.Split(rest, ",")
	if len(parts) < 2 {
		return arcTraceOperation{}, fmt.Errorf("incomplete operation fields")
	}

	opType := strings.TrimSpace(parts[0])
	var keyHex, valueHex string
	valueSize := -1
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "key:"):
			keyHex = strings.TrimSpace(strings.TrimPrefix(part, "key:"))
		case strings.HasPrefix(part, "value:"):
			valueHex = strings.TrimSpace(strings.TrimPrefix(part, "value:"))
		case strings.HasPrefix(part, "valuesize:"):
			parsed := strings.TrimSpace(strings.TrimPrefix(part, "valuesize:"))
			size, err := strconv.Atoi(parsed)
			if err != nil {
				return arcTraceOperation{}, fmt.Errorf("invalid valuesize %q: %w", parsed, err)
			}
			valueSize = size
		}
	}

	if keyHex == "" {
		return arcTraceOperation{}, fmt.Errorf("missing key field")
	}

	return arcTraceOperation{
		typeName:  opType,
		keyHex:    strings.TrimSpace(keyHex),
		valueHex:  strings.TrimSpace(valueHex),
		valueSize: valueSize,
	}, nil
}

func arcReplayGet(cacheEngine *arc.Cache, keyHex string, valueSize int) bool {
	if keyHex == "" {
		return false
	}
	if _, ok := cacheEngine.Get(keyHex); ok {
		return true
	}
	if valueSize >= 0 {
		cacheEngine.Put(keyHex, make([]byte, valueSize))
	}
	return false
}

func arcReplayPut(cacheEngine *arc.Cache, keyHex, valueHex string) error {
	if keyHex == "" {
		return fmt.Errorf("empty key for put")
	}
	if valueHex == "" {
		return fmt.Errorf("empty value for put")
	}

	if _, err := hex.DecodeString(keyHex); err != nil {
		return fmt.Errorf("decode key: %w", err)
	}
	valueBytes, err := hex.DecodeString(valueHex)
	if err != nil {
		return fmt.Errorf("decode value: %w", err)
	}

	cacheEngine.Put(keyHex, valueBytes)
	return nil
}

type arcMetricsRecorder struct {
	mode         string
	file         *os.File
	writer       *csv.Writer
	totalGets    int
	hitGets      int
	totalLatency time.Duration
	latencyUs    []float64
}

func newArcMetricsRecorder(path string, mode string) (*arcMetricsRecorder, error) {
	mode = normalizeArcMetricsMode(mode)
	recorder := &arcMetricsRecorder{mode: mode}
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

func (r *arcMetricsRecorder) RecordGet(key string, hit bool, latency time.Duration) {
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
		arcHitLabel(hit),
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

func (r *arcMetricsRecorder) HitRate() float64 {
	if r == nil || r.totalGets == 0 {
		return 0
	}
	return float64(r.hitGets) / float64(r.totalGets)
}

func (r *arcMetricsRecorder) AverageLatencyUs() float64 {
	if r == nil || r.totalGets == 0 {
		return 0
	}
	return float64(r.totalLatency) / float64(time.Microsecond) / float64(r.totalGets)
}

func (r *arcMetricsRecorder) P95LatencyUs() float64 {
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

func (r *arcMetricsRecorder) Close() error {
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

func arcHitLabel(hit bool) string {
	if hit {
		return "hit"
	}
	return "miss"
}

type arcRunSummaryRow struct {
	TimestampMS   int64
	DurationSec   float64
	TracePath     string
	StartBlock    int64
	EndBlock      int64
	CacheBytes    int64
	CapacityBytes int64
	Gets          int
	Hits          int
	HitRate       float64
	AvgLatencyUS  float64
	P95LatencyUS  float64
	Puts          int
	Failures      int
	Evictions     int64
	B1Hits        int64
	B2Hits        int64
	TargetPBytes  int64
	TotalBytes    int64
	T1Bytes       int64
	T2Bytes       int64
	B1Bytes       int64
	B2Bytes       int64
	T1Entries     int
	T2Entries     int
	B1Entries     int
	B2Entries     int
	MetricsMode   string
	IntervalOps   int
}

func writeArcRunSummary(path string, row arcRunSummaryRow) error {
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
		"timestamp_ms", "duration_sec", "trace", "start_block", "end_block", "cache_bytes", "capacity_bytes",
		"get_total", "get_hits", "get_hit_rate", "get_latency_avg_us", "get_latency_p95_us",
		"put_total", "failures",
		"evictions_total", "b1_hits", "b2_hits", "target_p_bytes",
		"total_bytes_end", "t1_bytes_end", "t2_bytes_end", "b1_bytes_end", "b2_bytes_end",
		"t1_entries_end", "t2_entries_end", "b1_entries_end", "b2_entries_end",
		"metrics_mode", "interval_ops",
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
		strconv.FormatInt(row.CapacityBytes, 10),
		strconv.Itoa(row.Gets),
		strconv.Itoa(row.Hits),
		fmt.Sprintf("%.6f", row.HitRate),
		fmt.Sprintf("%.6f", row.AvgLatencyUS),
		fmt.Sprintf("%.6f", row.P95LatencyUS),
		strconv.Itoa(row.Puts),
		strconv.Itoa(row.Failures),
		strconv.FormatInt(row.Evictions, 10),
		strconv.FormatInt(row.B1Hits, 10),
		strconv.FormatInt(row.B2Hits, 10),
		strconv.FormatInt(row.TargetPBytes, 10),
		strconv.FormatInt(row.TotalBytes, 10),
		strconv.FormatInt(row.T1Bytes, 10),
		strconv.FormatInt(row.T2Bytes, 10),
		strconv.FormatInt(row.B1Bytes, 10),
		strconv.FormatInt(row.B2Bytes, 10),
		strconv.Itoa(row.T1Entries),
		strconv.Itoa(row.T2Entries),
		strconv.Itoa(row.B1Entries),
		strconv.Itoa(row.B2Entries),
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

type arcIntervalRecorder struct {
	file            *os.File
	writer          *csv.Writer
	intervalOps     int
	ops             int
	intervalGets    int
	intervalHits    int
	intervalPuts    int
	intervalLatency time.Duration
	latencyUs       []float64
	lastEvictions   int64
}

func newArcIntervalRecorder(path string, intervalOps int) (*arcIntervalRecorder, error) {
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
		"evictions_interval", "evictions_total", "b1_hits_total", "b2_hits_total", "target_p_bytes",
		"total_bytes", "t1_bytes", "t2_bytes", "b1_bytes", "b2_bytes",
		"t1_entries", "t2_entries", "b1_entries", "b2_entries",
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

	return &arcIntervalRecorder{
		file:        file,
		writer:      writer,
		intervalOps: intervalOps,
	}, nil
}

func (r *arcIntervalRecorder) RecordGet(hit bool, latency time.Duration) {
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

func (r *arcIntervalRecorder) RecordPut() {
	if r == nil {
		return
	}
	r.ops++
	r.intervalPuts++
}

func (r *arcIntervalRecorder) ShouldFlush() bool {
	if r == nil || r.intervalOps <= 0 {
		return false
	}
	return r.ops >= r.intervalOps
}

func (r *arcIntervalRecorder) Flush(cumulativeHitRate float64, stats arc.Stats) error {
	if r == nil {
		return nil
	}
	if !r.ShouldFlush() {
		return nil
	}
	return r.flush(cumulativeHitRate, stats)
}

func (r *arcIntervalRecorder) FlushRemainder(cumulativeHitRate float64, stats arc.Stats) error {
	if r == nil || r.ops == 0 {
		return nil
	}
	return r.flush(cumulativeHitRate, stats)
}

func (r *arcIntervalRecorder) flush(cumulativeHitRate float64, stats arc.Stats) error {
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
	p95 := arcPercentile95(r.latencyUs)
	evictionsDelta := int64(0)
	if stats.Evictions >= r.lastEvictions {
		evictionsDelta = stats.Evictions - r.lastEvictions
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
		strconv.FormatInt(evictionsDelta, 10),
		strconv.FormatInt(stats.Evictions, 10),
		strconv.FormatInt(stats.B1Hits, 10),
		strconv.FormatInt(stats.B2Hits, 10),
		strconv.FormatInt(stats.TargetPBytes, 10),
		strconv.FormatInt(stats.TotalBytes, 10),
		strconv.FormatInt(stats.T1Bytes, 10),
		strconv.FormatInt(stats.T2Bytes, 10),
		strconv.FormatInt(stats.B1Bytes, 10),
		strconv.FormatInt(stats.B2Bytes, 10),
		strconv.Itoa(stats.T1Entries),
		strconv.Itoa(stats.T2Entries),
		strconv.Itoa(stats.B1Entries),
		strconv.Itoa(stats.B2Entries),
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
	r.lastEvictions = stats.Evictions
	return nil
}

func (r *arcIntervalRecorder) Close() error {
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

func normalizeArcMetricsMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "full" {
		return "full"
	}
	return "summary"
}

func arcPercentile95(vals []float64) float64 {
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

func normalizeArcByteCapacity(v int64) int64 {
	if v <= 0 {
		return 0
	}
	return v
}
