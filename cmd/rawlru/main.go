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

type lruCache struct {
	lru       gethcache.BasicLRU[string, int]
	capacity  int64
	bytesUsed int64
}

func newLRUCache(capacityBytes int64) *lruCache {
	if capacityBytes < 0 {
		capacityBytes = 0
	}
	entryCap := int(capacityBytes)
	if entryCap <= 0 {
		entryCap = 1
	}
	return &lruCache{
		lru:      gethcache.NewBasicLRU[string, int](entryCap),
		capacity: capacityBytes,
	}
}

func (c *lruCache) Get(key string) bool {
	if c == nil || key == "" {
		return false
	}
	_, ok := c.lru.Get(key)
	return ok
}

func (c *lruCache) Put(key string, valueSize int) {
	if c == nil || key == "" || valueSize < 0 {
		return
	}
	size := int64(valueSize)

	if c.capacity == 0 || size > c.capacity {
		if oldSize, ok := c.lru.Peek(key); ok && c.lru.Remove(key) {
			c.bytesUsed -= int64(oldSize)
			if c.bytesUsed < 0 {
				c.bytesUsed = 0
			}
		}
		return
	}

	if oldSize, ok := c.lru.Peek(key); ok {
		c.bytesUsed -= int64(oldSize)
		if c.bytesUsed < 0 {
			c.bytesUsed = 0
		}
	}

	for c.bytesUsed+size > c.capacity {
		_, evictedSize, ok := c.lru.RemoveOldest()
		if !ok {
			break
		}
		c.bytesUsed -= int64(evictedSize)
		if c.bytesUsed < 0 {
			c.bytesUsed = 0
		}
	}

	c.lru.Add(key, valueSize)
	c.bytesUsed += size
}

func (c *lruCache) Entries() int {
	if c == nil {
		return 0
	}
	return c.lru.Len()
}

func main() {
	var (
		tracePath  = flag.String("trace", "", "path to trace log file")
		cacheSize  = flag.Int64("cache", 1<<30, "cache size in bytes")
		startBlock = flag.Int64("startBlock", -1, "The starting block number to process.")
		endBlock   = flag.Int64("endBlock", -1, "The ending block number to process.")
	)
	flag.Parse()

	if *tracePath == "" {
		log.Fatal("trace log path must be provided via -trace")
	}

	cacheEngine := newLRUCache(*cacheSize)

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
		processing = (*startBlock == -1)
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines++

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
			hit := replayGet(cacheEngine, op.keyHex, op.valueSize)
			metrics.RecordGet(hit, time.Since(start))
			if hit {
				getHits++
			}
		case "put":
			putOps++
			if err := replayPut(cacheEngine, op.keyHex, op.valueHex); err != nil {
				failures++
				log.Printf("put error on line %d: %v", lines, err)
			}
		case "batchput":
			putOps++
			if err := replayPut(cacheEngine, op.keyHex, op.valueHex); err != nil {
				failures++
				log.Printf("batchput error on line %d: %v", lines, err)
			}
		default:
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("read trace: %v", err)
	}

	hitRate := 0.0
	if getOps > 0 {
		hitRate = float64(getHits) / float64(getOps)
	}
	avgLatency := metrics.AverageLatencyUs()
	p95Latency := metrics.P95LatencyUs()

	log.Printf("raw lru stats: capacity_bytes=%d bytes_used=%d entries=%d", cacheEngine.capacity, cacheEngine.bytesUsed, cacheEngine.Entries())
	log.Printf("replay complete: %d lines processed, %d GET ops (%d hits, %.2f%%), %d PUT/BATCHPUT ops, valuesize<0 events %d, %d failures, avg latency %.3f us, p95 latency %.3f us", lines, getOps, getHits, hitRate*100, putOps, negSizeOps, failures, avgLatency, p95Latency)
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

func replayGet(cacheEngine *lruCache, keyHex string, valueSize int) bool {
	if keyHex == "" {
		return false
	}

	if cacheEngine.Get(keyHex) {
		return true
	}
	if valueSize >= 0 {
		cacheEngine.Put(keyHex, valueSize)
	}
	return false
}

func replayPut(cacheEngine *lruCache, keyHex, valueHex string) error {
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

	cacheEngine.Put(keyHex, len(valueBytes))
	return nil
}
