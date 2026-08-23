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

type analysisStats struct {
	getOps   int
	putOps   int
	batchOps int

	uniqueAll map[string]struct{}
	uniqueGet map[string]struct{}
	uniquePut map[string]struct{}

	getFreqByKey map[string]int
	getTotalByKV map[string]int
}

func newAnalysisStats() *analysisStats {
	return &analysisStats{
		uniqueAll:    make(map[string]struct{}),
		uniqueGet:    make(map[string]struct{}),
		uniquePut:    make(map[string]struct{}),
		getFreqByKey: make(map[string]int),
		getTotalByKV: make(map[string]int),
	}
}

func main() {
	var (
		tracePath = flag.String("trace", "../../debug_trace/sampled-filtered-50K", "path to trace log file")
		outDir    = flag.String("outdir", ".", "directory to write analysis logs")
	)
	flag.Parse()

	stats, err := analyzeTrace(*tracePath)
	if err != nil {
		log.Fatalf("analyze trace: %v", err)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	timestamp := time.Now().Format("20060102_150405")

	masterPath := filepath.Join(*outDir, fmt.Sprintf("%s-master.log", timestamp))
	if err := writeMasterLog(masterPath, stats, *tracePath); err != nil {
		log.Fatalf("write master log: %v", err)
	}

	globalFreqPath := filepath.Join(*outDir, fmt.Sprintf("%s-GET-freq-distribution.log", timestamp))
	if err := writeFreqDistributionFile(globalFreqPath, stats.getFreqByKey); err != nil {
		log.Fatalf("write global GET frequency distribution: %v", err)
	}

	if err := writePerClassDistributions(*outDir, timestamp, stats.getFreqByKey); err != nil {
		log.Fatalf("write per-class GET frequency distributions: %v", err)
	}

	log.Printf("analysis complete. master log: %s", masterPath)
	log.Printf("analysis complete. global GET frequency log: %s", globalFreqPath)
}

func analyzeTrace(tracePath string) (*analysisStats, error) {
	const progressInterval = 10000

	f, err := os.Open(tracePath)
	if err != nil {
		return nil, fmt.Errorf("open trace file %s: %w", tracePath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	stats := newAnalysisStats()
	totalLines := 0

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

		key := strings.TrimSpace(strings.ToLower(op.keyHex))
		if key == "" {
			continue
		}

		stats.uniqueAll[key] = struct{}{}

		switch strings.ToLower(op.typeName) {
		case "get":
			stats.getOps++
			stats.uniqueGet[key] = struct{}{}
			stats.getFreqByKey[key]++

			kvClass := utils.GetCategoryName(key)
			stats.getTotalByKV[kvClass]++
		case "put":
			stats.putOps++
			stats.uniquePut[key] = struct{}{}
		case "batchput":
			stats.batchOps++
			stats.uniquePut[key] = struct{}{}
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

func writeMasterLog(path string, stats *analysisStats, tracePath string) error {
	onlyGetOnceTotal := 0
	onlyGetOnceByKV := make(map[string]int)
	perClassFreqs := make(map[string]map[int]int)

	for key, freq := range stats.getFreqByKey {
		kvClass := utils.GetCategoryName(key)
		if _, ok := perClassFreqs[kvClass]; !ok {
			perClassFreqs[kvClass] = make(map[int]int)
		}
		perClassFreqs[kvClass][freq]++

		if freq == 1 {
			onlyGetOnceTotal++
			onlyGetOnceByKV[kvClass]++
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create master log %s: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	fmt.Fprintf(w, "Trace Analysis Summary\n")
	fmt.Fprintf(w, "Input trace: %s\n", tracePath)
	fmt.Fprintln(w)

	totalPuts := stats.putOps + stats.batchOps
	fmt.Fprintf(w, "Operation counts:\n")
	fmt.Fprintf(w, "GET: %d\n", stats.getOps)
	fmt.Fprintf(w, "PUT: %d\n", stats.putOps)
	fmt.Fprintf(w, "BatchPUT: %d\n", stats.batchOps)
	fmt.Fprintf(w, "PUT+BatchPUT: %d\n", totalPuts)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Unique key counts:\n")
	fmt.Fprintf(w, "All operations: %d\n", len(stats.uniqueAll))
	fmt.Fprintf(w, "GET operations: %d\n", len(stats.uniqueGet))
	fmt.Fprintf(w, "PUT/BatchPUT operations: %d\n", len(stats.uniquePut))
	fmt.Fprintln(w)

	fmt.Fprintf(w, "KV pairs only GET once:\n")
	fmt.Fprintf(w, "Total: %d\n", onlyGetOnceTotal)
	fmt.Fprintf(w, "By KV class:\n")
	for _, className := range sortedKeysIntMap(onlyGetOnceByKV) {
		fmt.Fprintf(w, "%s: %d\n", className, onlyGetOnceByKV[className])
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Total GET frequencies by KV class:\n")
	for _, className := range sortedKeysIntMap(stats.getTotalByKV) {
		fmt.Fprintf(w, "%s: %d\n", className, stats.getTotalByKV[className])
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Per-KV-class GET frequency distribution files are generated as:\n")
	fmt.Fprintf(w, "$timestamp$-$KV class name$-GET-freq-distribution.log\n")

	return nil
}

func writeFreqDistributionFile(path string, getFreqByKey map[string]int) error {
	freqBuckets := make(map[int]int)
	for _, freq := range getFreqByKey {
		freqBuckets[freq]++
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create distribution file %s: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	fmt.Fprintln(w, "GET frequency distribution")
	fmt.Fprintln(w, "frequency,key_count")
	for _, freq := range sortedIntKeys(freqBuckets) {
		fmt.Fprintf(w, "%d,%d\n", freq, freqBuckets[freq])
	}
	return nil
}

func writePerClassDistributions(outDir, timestamp string, getFreqByKey map[string]int) error {
	perClassKeyFreq := make(map[string]map[string]int)
	for key, freq := range getFreqByKey {
		className := utils.GetCategoryName(key)
		if _, ok := perClassKeyFreq[className]; !ok {
			perClassKeyFreq[className] = make(map[string]int)
		}
		perClassKeyFreq[className][key] = freq
	}

	for _, className := range sortedClassNames(perClassKeyFreq) {
		filename := fmt.Sprintf("%s-%s-GET-freq-distribution.log", timestamp, sanitizeForFilename(className))
		path := filepath.Join(outDir, filename)
		if err := writeFreqDistributionFile(path, perClassKeyFreq[className]); err != nil {
			return err
		}
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
