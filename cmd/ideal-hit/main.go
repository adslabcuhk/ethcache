package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"sim/utils"
)

type idealTraceOperation struct {
	typeName string
	keyHex   string
	valueHex string
}

type idealClassStats struct {
	Total int64
	Hits  int64
}

func main() {
	var (
		tracePath  = flag.String("trace", "", "path to trace log file")
		outputPath = flag.String("out", "ideal_cache_metrics.csv", "CSV output path")
		startBlock = flag.Int64("startBlock", -1, "starting block number to process")
		endBlock   = flag.Int64("endBlock", -1, "ending block number to process")
	)
	flag.Parse()

	if *tracePath == "" {
		log.Fatal("trace log path must be provided via -trace")
	}

	if err := runIdealCacheAnalysis(*tracePath, *outputPath, *startBlock, *endBlock); err != nil {
		log.Fatalf("ideal cache analysis failed: %v", err)
	}
}

func runIdealCacheAnalysis(tracePath, outputPath string, startBlock, endBlock int64) error {
	traceFile, err := os.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace file: %w", err)
	}
	defer traceFile.Close()

	scanner := bufio.NewScanner(traceFile)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	processing := (startBlock == -1)
	unlimitedCache := make(map[string]string)
	classStats := make(map[string]idealClassStats)

	var (
		lines   int64
		getOps  int64
		getHits int64
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines++

		if strings.Contains(line, "Processing block (start)") {
			blockID, parseErr := idealParseBlockID(line)
			log.Printf("Processing block (start) %d\n", blockID)
			
			if parseErr == nil {
				if !processing && blockID >= startBlock {
					processing = true
					log.Printf("started processing at block %d", blockID)
				}
				if processing && endBlock != -1 && blockID > endBlock {
					log.Printf("reached block %d, beyond endBlock %d", blockID, endBlock)
					break
				}
			}
		}

		if strings.Contains(line, "Processing block (end)") {
			blockID, parseErr := idealParseBlockID(line)
			log.Printf("Processing block (end) %d\n", blockID)

			if parseErr == nil && processing && endBlock != -1 && blockID == endBlock {
				log.Printf("finished processing at end of block %d", blockID)
				break
			}
		}

		if !processing {
			continue
		}

		op, parseErr := idealParseTraceLine(line)
		if parseErr != nil {
			continue
		}

		switch strings.ToLower(op.typeName) {
		case "get":
			getOps++
			className := utils.GetCategoryName(op.keyHex)
			stats := classStats[className]
			stats.Total++

			if _, ok := unlimitedCache[op.keyHex]; ok {
				getHits++
				stats.Hits++
			} else {
				// On a miss, treat it as loaded into this unlimited cache for future accesses.
				unlimitedCache[op.keyHex] = ""
			}

			classStats[className] = stats
		case "put":
			// Write allocation: insert or replace cached value, but don't count write hits.
			unlimitedCache[op.keyHex] = op.valueHex
			continue
		case "batchput":
			// Write allocation: insert or replace cached value, but don't count write hits.
			unlimitedCache[op.keyHex] = op.valueHex
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read trace: %w", err)
	}

	if err := writeIdealCSV(outputPath, getOps, getHits, classStats); err != nil {
		return err
	}

	overallHitRate := idealRate(getHits, getOps)
	log.Printf("analysis complete: lines=%d get_ops=%d get_hits=%d hit_rate=%.6f unique_cached_read_keys=%d output=%s",
		lines, getOps, getHits, overallHitRate, len(unlimitedCache), outputPath)

	return nil
}

func writeIdealCSV(outputPath string, totalGets, totalHits int64, classStats map[string]idealClassStats) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create csv: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{"scope", "class", "total_kv_pairs", "hit_kv_pairs", "hit_rate"}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	totalRow := []string{
		"total",
		"ALL",
		strconv.FormatInt(totalGets, 10),
		strconv.FormatInt(totalHits, 10),
		fmt.Sprintf("%.6f", idealRate(totalHits, totalGets)),
	}
	if err := w.Write(totalRow); err != nil {
		return fmt.Errorf("write total row: %w", err)
	}

	classes := make([]string, 0, len(classStats))
	for className := range classStats {
		classes = append(classes, className)
	}
	sort.Strings(classes)

	for _, className := range classes {
		stats := classStats[className]
		row := []string{
			"class",
			className,
			strconv.FormatInt(stats.Total, 10),
			strconv.FormatInt(stats.Hits, 10),
			fmt.Sprintf("%.6f", idealRate(stats.Hits, stats.Total)),
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("write class row for %s: %w", className, err)
		}
	}

	if err := w.Error(); err != nil {
		return fmt.Errorf("flush csv writer: %w", err)
	}

	return nil
}

func idealRate(hit, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

func idealParseBlockID(line string) (int64, error) {
	idIndex := strings.Index(line, "ID:")
	if idIndex == -1 {
		return 0, fmt.Errorf("missing 'ID:' in line")
	}
	idPart := line[idIndex+len("ID:"):]
	idStr := strings.Fields(idPart)[0]
	idStr = strings.TrimRight(idStr, ",")
	return strconv.ParseInt(idStr, 10, 64)
}

func idealParseTraceLine(line string) (idealTraceOperation, error) {
	idx := strings.Index(line, "OPType:")
	if idx == -1 {
		return idealTraceOperation{}, fmt.Errorf("missing OPType: prefix")
	}
	rest := line[idx+len("OPType:"):]
	parts := strings.Split(rest, ",")
	if len(parts) < 2 {
		return idealTraceOperation{}, fmt.Errorf("incomplete operation fields")
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
		return idealTraceOperation{}, fmt.Errorf("missing key field")
	}

	return idealTraceOperation{
		typeName: opType,
		keyHex:   strings.TrimSpace(keyHex),
		valueHex: strings.TrimSpace(valueHex),
	}, nil
}