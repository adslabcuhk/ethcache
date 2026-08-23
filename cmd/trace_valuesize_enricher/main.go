package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cockroachdb/pebble"
)

var getLineRegex = regexp.MustCompile(`OPType:\s*Get,\s*key:\s*([0-9a-fA-F]+),\s*size:\s*(\d+)`)
var putLineRegex = regexp.MustCompile(`OPType:\s*Put,\s*key:\s*([0-9a-fA-F]+),\s*size:\s*\d+,\s*value:\s*([0-9a-fA-F]+),\s*size:\s*\d+`)
var batchPutLineRegex = regexp.MustCompile(`OPType:\s*BatchPut,\s*key:\s*([0-9a-fA-F]+),\s*size:\s*\d+,\s*value:\s*([0-9a-fA-F]+),\s*size:\s*\d+`)

func main() {
	tracePath := flag.String("trace", "", "path to input trace file")
	dbPath := flag.String("db", "", "path to PebbleDB directory")
	outPath := flag.String("out", "", "path to output trace file")
	startBlock := flag.Int64("startBlock", -1, "start block number (inclusive) for enrichment")
	endBlock := flag.Int64("endBlock", -1, "end block number (inclusive) for enrichment")
	missingValueSize := flag.Int("missing-value-size", -1, "value size written when a GET key is missing in PebbleDB")
	failOnMissing := flag.Bool("fail-on-missing", false, "fail immediately if any GET key is missing in PebbleDB")
	flag.Parse()

	if *tracePath == "" || *dbPath == "" || *outPath == "" {
		flag.Usage()
		os.Exit(2)
	}

	if *startBlock != -1 && *endBlock != -1 && *startBlock > *endBlock {
		log.Fatalf("invalid block range: startBlock (%d) > endBlock (%d)", *startBlock, *endBlock)
	}

	if err := ensureDifferentPaths(*tracePath, *outPath); err != nil {
		log.Fatal(err)
	}

	if err := enrichTrace(*tracePath, *dbPath, *outPath, *startBlock, *endBlock, *missingValueSize, *failOnMissing); err != nil {
		log.Fatal(err)
	}
}

func enrichTrace(tracePath, dbPath, outPath string, startBlock, endBlock int64, missingValueSize int, failOnMissing bool) error {
	in, err := os.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	defer in.Close()

	db, err := pebble.Open(dbPath, &pebble.Options{ReadOnly: false})
	if err != nil {
		return fmt.Errorf("open pebbledb (read-write): %w", err)
	}

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create output trace: %w", err)
	}
	defer out.Close()

	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)

	var lineNum int
	var getCount int
	var missingCount int
	processing := (startBlock == -1)

	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("read trace line %d: %w", lineNum+1, readErr)
		}

		if line == "" && errors.Is(readErr, io.EOF) {
			break
		}

		lineNum++

		if strings.Contains(line, "Processing block (start)") {
			blockID, err := parseBlockID(line)
			log.Printf("encountered block start line for block %d at line %d", blockID, lineNum)
			if err == nil {
				if !processing && blockID >= startBlock {
					processing = true
					log.Printf("started enrichment at block %d", blockID)
				}
				if processing && endBlock != -1 && blockID > endBlock {
					log.Printf("finished enrichment: reached block start %d after endBlock %d", blockID, endBlock)
					break
				}
			}
		}

		enriched := line
		isGetLine := false
		wasMissing := false
		var err error
		if processing {
			enriched, isGetLine, wasMissing, err = enrichLine(line, db, lineNum, missingValueSize, failOnMissing)
		}
		if err != nil {
			return err
		}

		if isGetLine {
			getCount++
			if wasMissing {
				missingCount++
			}
		}

		if _, err := writer.WriteString(enriched); err != nil {
			return fmt.Errorf("write output line %d: %w", lineNum, err)
		}

		if strings.Contains(line, "Processing block (end)") {
			blockID, err := parseBlockID(line)
			log.Printf("encountered block end line for block %d at line %d", blockID, lineNum)
			if err == nil {
				if processing && endBlock != -1 && blockID == endBlock {
					log.Printf("finished enrichment at end of block %d", blockID)
					break
				}
			}
		}

		if errors.Is(readErr, io.EOF) {
			break
		}
	}

	log.Printf("finalizing output writer...")
	if err := writer.Flush(); err != nil {
		_ = db.Close()
		return fmt.Errorf("flush output writer: %w", err)
	}

	log.Printf("finalizing pebbledb (flush/close)...")
	if err := db.Flush(); err != nil {
		_ = db.Close()
		return fmt.Errorf("flush pebbledb: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close pebbledb: %w", err)
	}

	log.Printf("done: processed %d lines, enriched %d GET lines, missing keys %d", lineNum, getCount, missingCount)
	return nil
}

func enrichLine(line string, db *pebble.DB, lineNum, missingValueSize int, failOnMissing bool) (string, bool, bool, error) {
	if strings.Contains(line, "OPType: Put,") {
		if err := replayWriteLine(line, putLineRegex, db, lineNum, "Put"); err != nil {
			return "", false, false, err
		}
		return line, false, false, nil
	}

	if strings.Contains(line, "OPType: BatchPut,") {
		if err := replayWriteLine(line, batchPutLineRegex, db, lineNum, "BatchPut"); err != nil {
			return "", false, false, err
		}
		return line, false, false, nil
	}

	if strings.Contains(line, "valuesize:") || !strings.Contains(line, "OPType: Get,") {
		return line, false, false, nil
	}

	matches := getLineRegex.FindStringSubmatch(line)
	if matches == nil {
		return line, false, false, nil
	}

	keyHex := matches[1]
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return "", false, false, fmt.Errorf("line %d: decode key hex %q: %w", lineNum, keyHex, err)
	}

	value, closer, err := db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			if failOnMissing {
				return "", true, false, fmt.Errorf("line %d: key not found in PebbleDB: %s", lineNum, keyHex)
			}
			return appendValueSize(line, missingValueSize), true, true, nil
		}
		return "", true, false, fmt.Errorf("line %d: PebbleDB get failed for key %s: %w", lineNum, keyHex, err)
	}
	defer closer.Close()

	return appendValueSize(line, len(value)), true, false, nil
}

func replayWriteLine(line string, re *regexp.Regexp, db *pebble.DB, lineNum int, opType string) error {
	matches := re.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	keyHex := strings.TrimSpace(matches[1])
	valueHex := strings.TrimSpace(matches[2])

	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return fmt.Errorf("line %d: decode %s key hex %q: %w", lineNum, opType, keyHex, err)
	}

	value, err := hex.DecodeString(valueHex)
	if err != nil {
		return fmt.Errorf("line %d: decode %s value hex for key %s: %w", lineNum, opType, keyHex, err)
	}

	if err := db.Set(key, value, pebble.NoSync); err != nil {
		return fmt.Errorf("line %d: replay %s into PebbleDB for key %s: %w", lineNum, opType, keyHex, err)
	}

	return nil
}

func appendValueSize(line string, valueSize int) string {
	if strings.HasSuffix(line, "\n") {
		return strings.TrimSuffix(line, "\n") + ", valuesize: " + strconv.Itoa(valueSize) + "\n"
	}
	return line + ", valuesize: " + strconv.Itoa(valueSize)
}

func ensureDifferentPaths(input, output string) error {
	inputAbs, err := filepath.Abs(input)
	if err != nil {
		return fmt.Errorf("resolve input path: %w", err)
	}

	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}

	if inputAbs == outputAbs {
		return fmt.Errorf("input trace and output trace must be different files")
	}

	return nil
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