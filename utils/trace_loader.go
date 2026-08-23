package utils

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

func ProcessLogBatch(inputFile string, batchStartIDs, batchEndIDs []int) error {

	fmt.Printf("Processing %s\n", inputFile)

	// Open the input log file
	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("failed to open input file: %v", err)
	}
	defer file.Close()

	// Regex to match block start and end lines
	startRegex := regexp.MustCompile(`Processing block \(start\), ID: (\d+)`)
	endRegex := regexp.MustCompile(`Processing block \(end\), ID: (\d+)`)

	// Regex to match KV OPType lines
	opGetRegex := regexp.MustCompile(`OPType: Get, key: ([0-9a-fA-F]+), size: (\d+)`)

	// OPType: Put, key: 756e636c65616e2d73687574646f776e, size: 16, value: e080de84672c87818467a495958467a4991b8467a49a098467a8f1ec8467ab3216, size: 33
	opPutRegex := regexp.MustCompile(`OPType: Put, key: ([0-9a-fA-F]+), size: (\d+), value: ([0-9a-fA-F]+), size: (\d+)`)
	opBatchPutRegex := regexp.MustCompile(`OPType: BatchPut, key: ([0-9a-fA-F]+), size: (\d+), value: ([0-9a-fA-F]+), size: (\d+)`)

	// Variables to track the current block
	var currentBlockID string
	var endBlockID string

	// Create a buffered reader
	reader := bufio.NewReader(file)

	var lineCount uint64
	lineCount = 0

	var foundStartID bool
	foundStartID = false

	var batchIndex int
	batchIndex = 0

	for {
		// Read the file line by line
		lineCount++

		if lineCount%10000 == 0 {
			fmt.Printf("\rProcessed %d lines", lineCount)
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			// If we reach the end of the file, break the loop
			break
		}

		// Check if the line is the start of a block
		if matches := startRegex.FindStringSubmatch(line); matches != nil {
			// Extract the block ID
			currentBlockID = matches[1]

			// Convert block ID to integer
			blockIDInt, err := strconv.Atoi(currentBlockID)
			if err != nil {
				blockIDInt = 0
				fmt.Printf("Error converting current block ID to integer: %d\n", blockIDInt)
			}

			tmpBatchIndex := findIndex(blockIDInt, batchStartIDs)

			if tmpBatchIndex != -1 {
				batchIndex = tmpBatchIndex
				foundStartID = true
			}
		}

		// Skip all lines until we have found the line with startID
		if !foundStartID {
			continue
		}

		// If inside a block, check for "OPType: Get" lines
		if opGetRegex.MatchString(line) {
			fmt.Printf("Get OPT\n")
			if matches := opGetRegex.FindStringSubmatch(line); matches != nil {
				key := matches[1]
				size, err := strconv.Atoi(matches[2])
				if err != nil {
					fmt.Printf("Error converting size to integer: %v\n", err)
					continue
				}

				// EthCache API
				_, ok := EthCacheGet([]byte(key), size)
				if !ok {
					fmt.Printf("EthCache Get failed for key %s\n", key)
				}
			}
		}

		// If inside a block, check for "OPType: Put" lines
		if opPutRegex.MatchString(line) {
			fmt.Printf("Put OPT\n")
			if matches := opPutRegex.FindStringSubmatch(line); matches != nil {
				key := matches[1]
				// sizeStr := matches[2]
				value := matches[3]
				// valueSizeStr := matches[4]

				// EthCache API
				ok := EthCachePut([]byte(key), []byte(value))
				if !ok {
					fmt.Printf("EthCachePut failed for key %s\n", key)
				}
			}
		}

		// If inside a block, check for "OPType: Update" lines
		if opBatchPutRegex.MatchString(line) {
			fmt.Printf("BatchPut OPT\n")
			if matches := opBatchPutRegex.FindStringSubmatch(line); matches != nil {
				key := matches[1]
				// sizeStr := matches[2]
				value := matches[3]
				// valueSizeStr := matches[4]

				// EthCache API
				ok := EthCachePut([]byte(key), []byte(value))
				if !ok {
					fmt.Printf("EthCacheBatchPut failed for key %s\n", key)
				}
			}
		}

		// Check if the line is the end of the block
		if matches := endRegex.FindStringSubmatch(line); matches != nil {
			// Verify the block ID matches
			endBlockID := matches[1]
			if endBlockID != currentBlockID {
				return fmt.Errorf("block ID mismatch: start ID %s, end ID %s", currentBlockID, endBlockID)
			}

			endIDInt, err := strconv.Atoi(endBlockID)
			if err != nil {
				fmt.Println("Error converting end block ID to integer:", err)
			}

			// fmt.Printf("endIDInt %d\n", endIDInt)
			// fmt.Printf("batchEndId %d, %d\n", batchEndIDs[batchIndex], batchIndex)

			if endIDInt == batchEndIDs[batchIndex] {

				foundStartID = false

				// Print the final block ID in this batch process
				if endBlockID != currentBlockID {
					fmt.Printf("block ID mismatch: start ID %s, end ID %s\n", currentBlockID, endBlockID)
				} else {
					fmt.Printf("The final processed block ID in this batch is %s\n", currentBlockID)
				}

				// // Get the current memory usage
				// memoryUsage, err := GetMemoryUsage()
				// if err != nil {
				// 	fmt.Println("Get memory usage Error:", err)
				// }

				// fmt.Printf("Current memory usage: %d bytes (%.2f GiB)\n", memoryUsage, float64(memoryUsage)/1024/1024/1024)

				if endIDInt == batchEndIDs[len(batchEndIDs)-1] {
					return nil
				}
			}
		}
	}

	// Print the final block ID in this batch process
	if endBlockID != currentBlockID {
		fmt.Printf("block ID mismatch: start ID %s, end ID %s\n", currentBlockID, endBlockID)
	} else {
		fmt.Printf("The final processed block ID in this batch is %s\n", currentBlockID)
	}

	return nil
}

func GetMemoryUsage() (uint64, error) {
	// Read the process statm file
	pid := os.Getpid()
	statmFile := fmt.Sprintf("/proc/%d/statm", pid)
	data, err := os.ReadFile(statmFile)
	if err != nil {
		return 0, fmt.Errorf("failed to read statm file: %v", err)
	}

	// Parse the resident set size (in pages)
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, fmt.Errorf("invalid statm file format")
	}

	// Convert pages to bytes
	pageSize := uint64(os.Getpagesize())
	rssPages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse resident set size: %v", err)
	}

	return rssPages * pageSize, nil
}

func findIndex(target int, batchStartIDs []int) int {
	for index, id := range batchStartIDs {
		if id == target {
			return index // Return the index if the target is found
		}
	}
	return -1 // Return -1 if the target is not found
}

func EthCacheGet(key []byte, size int) ([]byte, bool) {
	// test prefix
	// var category string
	category := GetCategoryName(string(key))
	fmt.Printf("category name: %s\n", category)

	// test decode

	db, err := LoadKVSOptDB()
	if err != nil {
		fmt.Printf("Failed to load PebbleDB: %v\n", err)
		return nil, false
	}
	defer db.Close()
	return TestGet(key, size, db)
}

func EthCachePut(key []byte, value []byte) bool {
	db, err := LoadKVSOptDB()
	if err != nil {
		fmt.Printf("Failed to load PebbleDB: %v\n", err)
		return false
	}
	defer db.Close()
	// return TestPut(key, value, db)
	return true
}

func EthCacheUpdate(key []byte, value []byte) bool {
	db, err := LoadKVSOptDB()
	if err != nil {
		fmt.Printf("Failed to load PebbleDB: %v\n", err)
		return false
	}
	defer db.Close()
	// return TestUpdate(key, value, db)
	return true
}
