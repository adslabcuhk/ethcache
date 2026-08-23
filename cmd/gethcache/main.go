package main

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"sim/gethcache"
)

type traceOperation struct {
	typeName  string
	keyHex    string
	valueHex  string
	valueSize int
}

type metricsRecorder struct {
	totalGets    int
	hitGets      int
	totalLatency time.Duration
	latencyUs    []float64
}

type lowerCacheMetrics struct {
	totalReads int
	hitReads   int
}

const defaultLowerCacheSizeBytes int64 = 512 * 1024 * 1024

type lowerLevelCache struct {
	lru          gethcache.BasicLRU[string, int]
	capacity     int64
	currentUsage int64
}

func newLowerLevelCache(capacityBytes int64) *lowerLevelCache {
	if capacityBytes <= 0 {
		capacityBytes = defaultLowerCacheSizeBytes
	}
	entryCap := int(capacityBytes)
	if entryCap <= 0 {
		entryCap = 1
	}
	return &lowerLevelCache{
		lru:      gethcache.NewBasicLRU[string, int](entryCap),
		capacity: capacityBytes,
	}
}

func (c *lowerLevelCache) Get(key string) bool {
	if c == nil || key == "" {
		return false
	}
	_, ok := c.lru.Get(key)
	return ok
}

func (c *lowerLevelCache) Add(key string, valueSize int) {
	if c == nil || key == "" || valueSize < 0 {
		return
	}

	size := int64(valueSize)
	if size > c.capacity {
		if oldSize, ok := c.lru.Peek(key); ok && c.lru.Remove(key) {
			c.currentUsage -= int64(oldSize)
			if c.currentUsage < 0 {
				c.currentUsage = 0
			}
		}
		return
	}

	if oldSize, ok := c.lru.Peek(key); ok {
		c.currentUsage -= int64(oldSize)
		if c.currentUsage < 0 {
			c.currentUsage = 0
		}
	}

	for c.currentUsage+size > c.capacity {
		_, evictedSize, ok := c.lru.RemoveOldest()
		if !ok {
			break
		}
		c.currentUsage -= int64(evictedSize)
		if c.currentUsage < 0 {
			c.currentUsage = 0
		}
	}

	c.lru.Add(key, valueSize)
	c.currentUsage += size
}

func (r *metricsRecorder) RecordGet(hit bool, latency time.Duration) {
	if r == nil {
		return
	}
	r.totalGets++
	if hit {
		r.hitGets++
	}
	r.totalLatency += latency
	r.latencyUs = append(r.latencyUs, float64(latency)/float64(time.Microsecond))
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

func main() {
	var (
		tracePath       = flag.String("trace", "", "path to trace log file")
		cacheSize       = flag.Int64("cache", 1<<30, "cache size in bytes")
		metricsPath     = flag.String("metrics", "cache_metrics.csv", "metrics CSV output path")
		divide          = flag.Int64("divide", 1, "cache-class divisor for code/header/body/receipt/txlookup caches")
		multiply        = flag.Int64("multiply", 1, "cache-class multiplier for code/header/body/receipt/txlookup caches")
		lowerCacheBytes = flag.Int64("lowerCacheBytes", defaultLowerCacheSizeBytes, "lower-level cache size in bytes")
		startBlock      = flag.Int64("startBlock", -1, "The starting block number to process.")
		endBlock        = flag.Int64("endBlock", -1, "The ending block number to process.")
	)
	flag.Parse()

	if *tracePath == "" {
		log.Fatal("trace log path must be provided via -trace")
	}
	if *divide <= 0 {
		log.Fatal("-divide must be positive")
	}
	if *multiply <= 0 {
		log.Fatal("-multiply must be positive")
	}

	manager, err := gethcache.NewCacheManager(*cacheSize, *metricsPath, *divide, *multiply)
	if err != nil {
		log.Fatalf("create cache manager: %v", err)
	}
	lowerCache := newLowerLevelCache(*lowerCacheBytes)

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
		metrics    metricsRecorder
		lowerStats lowerCacheMetrics
		processing = (*startBlock == -1)
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
			// Non-operation lines (for example block markers) are skipped.
			continue
		}

		switch strings.ToLower(op.typeName) {
		case "get":
			// getOps++
			if op.valueSize < 0 {
				negSizeOps++
				break
			}

			getOps++
			start := time.Now()
			hit := replayGet(manager, lowerCache, &lowerStats, op.keyHex, op.valueSize)
			metrics.RecordGet(hit, time.Since(start))
			if hit {
				getHits++
			}
		case "put":
			putOps++
			if err := replayPut(manager, lowerCache, op.keyHex, op.valueHex); err != nil {
				failures++
				log.Printf("put error on line %d: %v", lines, err)
			}
		case "batchput":
			putOps++
			if err := replayPut(manager, lowerCache, op.keyHex, op.valueHex); err != nil {
				failures++
				log.Printf("batchput error on line %d: %v", lines, err)
			}
		default:
			// log.Printf("unknown operation '%s' on line %d", op.typeName, lines)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("read trace: %v", err)
	}

	if err := manager.FlushMetrics(); err != nil {
		log.Printf("flush metrics: %v", err)
	}
	logCacheRuntimeStats(manager)
	avgLatency := metrics.AverageLatencyUs()
	p95Latency := metrics.P95LatencyUs()

	log.Printf("replay complete: %d lines processed, %d GET ops (%d hits), %d PUT/BATCHPUT ops, valuesize<0 events %d, lower-cache reads %d (%d hits), %d failures, avg latency %.3f us, p95 latency %.3f us", lines, getOps, getHits, putOps, negSizeOps, lowerStats.totalReads, lowerStats.hitReads, failures, avgLatency, p95Latency)

	// 2026/01/04 01:39:29 replay complete: 17833 lines processed, 9984 GET ops (1637 hits), 7758 PUT/BATCHPUT ops, 18 failures
	// 2026/01/04 11:11:20 replay complete: 17833 lines processed, 9984 GET ops (3568 hits), 7758 PUT/BATCHPUT ops, 18 failures
	// 2026/01/04 12:41:14 replay complete: 17833 lines processed, 9984 GET ops (3568 hits), 7758 PUT/BATCHPUT ops, 18 failures
}

func parseBlockID(line string) (int64, error) {
	idIndex := strings.Index(line, "ID:")
	if idIndex == -1 {
		return 0, fmt.Errorf("missing 'ID:' in line")
	}
	idPart := line[idIndex+len("ID:"):]
	idStr := strings.Fields(idPart)[0]
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
				return traceOperation{}, fmt.Errorf("invalid valuesize %q: %w", parsed, err)
			}
			valueSize = size
		}
	}

	if keyHex == "" {
		return traceOperation{}, fmt.Errorf("missing key field")
	}

	return traceOperation{
		typeName:  opType,
		keyHex:    strings.TrimSpace(keyHex),
		valueHex:  strings.TrimSpace(valueHex),
		valueSize: valueSize,
	}, nil
}

func replayGet(manager *gethcache.CacheManager, lowerCache *lowerLevelCache, lowerStats *lowerCacheMetrics, keyHex string, valueSize int) bool {
	if keyHex == "" {
		return false
	}

	if _, ok := manager.GetHex(keyHex); ok {
		return true
	}

	if lowerStats != nil {
		lowerStats.totalReads++
	}

	if lowerCache != nil && lowerCache.Get(keyHex) {
		if lowerStats != nil {
			lowerStats.hitReads++
		}
		if valueSize >= 0 {
			manager.PutHex(keyHex, make([]byte, valueSize))
		}
		return false
	}

	if valueSize >= 0 {
		lowerCache.Add(keyHex, valueSize)
		manager.PutHex(keyHex, make([]byte, valueSize))
	}
	return false
}

func replayPut(manager *gethcache.CacheManager, lowerCache *lowerLevelCache, keyHex, valueHex string) error {
	if keyHex == "" {
		return fmt.Errorf("empty key for put")
	}
	if valueHex == "" {
		return fmt.Errorf("empty value for put")
	}

	_, err := hex.DecodeString(keyHex)
	if err != nil {
		return fmt.Errorf("decode key: %w", err)
	}
	valueBytes, err := hex.DecodeString(valueHex)
	if err != nil {
		return fmt.Errorf("decode value: %w", err)
	}

	if lowerCache != nil {
		lowerCache.Add(keyHex, len(valueBytes))
	}
	manager.PutHex(keyHex, valueBytes)
	return nil
}

func logCacheRuntimeStats(manager *gethcache.CacheManager) {
	stats := manager.CacheRuntimeStats()
	if len(stats.Classes) == 0 {
		return
	}
	classes := make([]string, 0, len(stats.Classes))
	for class := range stats.Classes {
		classes = append(classes, string(class))
	}
	sort.Strings(classes)

	for _, name := range classes {
		class := gethcache.CacheClass(name)
		s := stats.Classes[class]
		if s.LimitKind == "entries" {
			log.Printf("cache stats: class=%s limit=entries(%d) current_entries=%d max_entries=%d current_bytes=%d max_bytes=%d",
				name, s.CapacityEntries, s.CurrentEntries, s.MaxEntries, s.CurrentBytes, s.MaxBytes)
			continue
		}
		log.Printf("cache stats: class=%s limit=bytes(%d) current_entries=%d max_entries=%d current_bytes=%d max_bytes=%d",
			name, s.CapacityBytes, s.CurrentEntries, s.MaxEntries, s.CurrentBytes, s.MaxBytes)
	}
}
