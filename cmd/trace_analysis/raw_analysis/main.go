package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sim/utils"
)

type traceOperation struct {
	typeName  string
	keyHex    string
	valueHex  string
	valueSize int
}

type rawStats struct {
	totalRawHits   int
	rawHitsByClass map[string]int
	keyRawHitCount map[string]int
	seenClasses    map[string]struct{}
}

func newRawStats() *rawStats {
	return &rawStats{
		rawHitsByClass: make(map[string]int),
		keyRawHitCount: make(map[string]int),
		seenClasses:    make(map[string]struct{}),
	}
}

func main() {
	var (
		tracePath  = flag.String("trace", "../../../debug_trace/sampled-filtered-50K", "path to trace log file")
		outDir     = flag.String("outdir", ".", "directory to write analysis logs")
		rawWindow  = flag.Int("raw-window", 5000, "maximum operation distance to count as read-after-write hit")
	)
	flag.Parse()

	if *rawWindow < 0 {
		log.Fatalf("raw-window must be non-negative")
	}

	stats, err := analyzeRAW(*tracePath, *rawWindow)
	if err != nil {
		log.Fatalf("analyze RAW: %v", err)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	timestamp := time.Now().Format("20060102_150405")

	masterPath := filepath.Join(*outDir, fmt.Sprintf("%s-raw-window-%d-RAW-master.log", timestamp, *rawWindow))
	if err := writeRawMasterLog(masterPath, stats, *tracePath, *rawWindow); err != nil {
		log.Fatalf("write RAW master log: %v", err)
	}

	if err := writeRawPerClassDistributionsWithClasses(*outDir, timestamp, *rawWindow, stats.keyRawHitCount, stats.seenClasses); err != nil {
		log.Fatalf("write RAW per-class distributions: %v", err)
	}

	log.Printf("RAW analysis complete. master log: %s", masterPath)
}

func analyzeRAW(tracePath string, rawWindow int) (*rawStats, error) {
	const progressInterval = 10000

	f, err := os.Open(tracePath)
	if err != nil {
		return nil, fmt.Errorf("open trace file %s: %w", tracePath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	stats := newRawStats()
	recentWrites := make(map[string]int)

	totalLines := 0
	opIndex := 0

	for scanner.Scan() {
		totalLines++
		if totalLines%progressInterval == 0 {
			fmt.Printf("\rProcessed lines: %d", totalLines)
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		op, err := parseTraceLine(line)
		if err != nil {
			continue
		}

		opIndex++
		key := strings.TrimSpace(strings.ToLower(op.keyHex))
		if key == "" {
			continue
		}

		className := utils.GetCategoryName(key)
		stats.seenClasses[className] = struct{}{}

		switch strings.ToLower(op.typeName) {
		case "put", "batchput":
			recentWrites[key] = opIndex
		case "get":
			putIndex, ok := recentWrites[key]
			if !ok {
				continue
			}
			delta := opIndex - putIndex
			if delta < rawWindow {
				stats.totalRawHits++
				stats.rawHitsByClass[className]++
				stats.keyRawHitCount[key]++
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read trace: %w", err)
	}

	fmt.Printf("\rProcessed lines: %d\n", totalLines)
	return stats, nil
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

func writeRawMasterLog(path string, stats *rawStats, tracePath string, rawWindow int) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create RAW master log %s: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	fmt.Fprintln(w, "RAW Analysis Summary")
	fmt.Fprintf(w, "Input trace: %s\n", tracePath)
	fmt.Fprintf(w, "RAW threshold (operation distance): %d\n", rawWindow)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Total RAW hits: %d\n", stats.totalRawHits)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "RAW hits by KV prefix:")
	for _, className := range sortedKeysIntMap(stats.rawHitsByClass) {
		fmt.Fprintf(w, "%s: %d\n", className, stats.rawHitsByClass[className])
	}

	return nil
}

func writeRawPerClassDistributionsWithClasses(outDir, timestamp string, rawWindow int, keyRawHitCount map[string]int, seenClasses map[string]struct{}) error {
	perClassKeyHitCount := make(map[string]map[string]int)
	for key, count := range keyRawHitCount {
		className := utils.GetCategoryName(key)
		if _, ok := perClassKeyHitCount[className]; !ok {
			perClassKeyHitCount[className] = make(map[string]int)
		}
		perClassKeyHitCount[className][key] = count
	}

	if seenClasses != nil {
		for className := range seenClasses {
			if _, ok := perClassKeyHitCount[className]; !ok {
				perClassKeyHitCount[className] = make(map[string]int)
			}
		}
	}

	for _, className := range sortedClassNames(perClassKeyHitCount) {
		filename := fmt.Sprintf("%s-raw-window-%d-RAW-%s.log", timestamp, rawWindow, sanitizeForFilename(className))
		path := filepath.Join(outDir, filename)
		if err := writeFreqDistributionFile(path, perClassKeyHitCount[className]); err != nil {
			return err
		}
	}
	return nil
}

func writeFreqDistributionFile(path string, keyCount map[string]int) error {
	freqBuckets := make(map[int]int)
	for _, freq := range keyCount {
		freqBuckets[freq]++
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create distribution file %s: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	fmt.Fprintln(w, "RAW hit frequency distribution")
	fmt.Fprintln(w, "frequency,key_count")
	for _, freq := range sortedIntKeys(freqBuckets) {
		fmt.Fprintf(w, "%d,%d\n", freq, freqBuckets[freq])
	}
	return nil
}

func sanitizeForFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		" ", "_",
		":", "_",
		";", "_",
	)
	out := replacer.Replace(name)
	if out == "" {
		return "Unknown"
	}
	return out
}

func sortedIntKeys(m map[int]int) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func sortedKeysIntMap(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedClassNames(m map[string]map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
