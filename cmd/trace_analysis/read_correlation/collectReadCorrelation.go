package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// PairInfo stores the frequency and the list of BlockIDs where the pair appears
type PairInfo struct {
	Frequency int
	BlockIDs  string // BlockIDs are stored as a semicolon-separated string
}

func ProcessLogBatch(inputFile string, distance int, batchStartIDs, batchEndIDs []int, outputPathPrefix string) error {

	fmt.Printf("Processing %s, distance=%d\n", inputFile, distance)

	// Open the input log file
	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("failed to open input file: %v", err)
	}
	defer file.Close()

	// Regex to match block start and end lines
	startRegex := regexp.MustCompile(`Processing block \(start\), ID: (\d+)`)
	endRegex := regexp.MustCompile(`Processing block \(end\), ID: (\d+)`)
	opGetRegex := regexp.MustCompile(`OPType: Get, key: ([0-9a-fA-F]+), size: (\d+)`)

	// Variables to track the current block
	var currentBlockID string
	var endBlockID string
	var outputFile *os.File

	if outputFile != nil {
		outputFile.Close()
	}

	// Global frequency map to store results across all blocks
	globalFrequencyMap := make(map[string]PairInfo)
	var opGetLines []string // Slice to store "OPType: Get" lines within the current block

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

			opGetLines = nil // Reset the slice for the new block

			// Convert block ID to integer
			blockIDInt, err := strconv.Atoi(currentBlockID)
			if err != nil {
				blockIDInt = 0
				fmt.Println("Error converting current block ID to integer: %d", err, blockIDInt)
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
			opGetLines = append(opGetLines, line) // Store the line for frequency calculation

			// Update the global frequency map
			if len(opGetLines) > distance+1 {
				// Get the two lines at the specified distance
				line1 := opGetLines[len(opGetLines)-distance-2]
				line2 := opGetLines[len(opGetLines)-1]

				// Extract key and size from line1
				matches1 := opGetRegex.FindStringSubmatch(line1)
				if len(matches1) != 3 {
					return fmt.Errorf("failed to parse key and size from line: %s", line1)
				}
				key1 := matches1[1]
				size1 := matches1[2]

				// Extract key and size from line2
				matches2 := opGetRegex.FindStringSubmatch(line2)
				if len(matches2) != 3 {
					return fmt.Errorf("failed to parse key and size from line: %s", line2)
				}
				key2 := matches2[1]
				size2 := matches2[2]

				// Create a unique key for the pair (order-independent)
				pairKey := fmt.Sprintf("%s-%s;%s-%s", key1, size1, key2, size2)
				if key1 > key2 {
					pairKey = fmt.Sprintf("%s-%s;%s-%s", key2, size2, key1, size1)
				}

				// Update the frequency and BlockID list for this pair
				if pairInfo, exists := globalFrequencyMap[pairKey]; exists {
					// If the pair already exists, update the frequency and BlockID list
					pairInfo.Frequency++
					if !strings.Contains(pairInfo.BlockIDs, currentBlockID) {
						pairInfo.BlockIDs += ";" + currentBlockID
					}
					globalFrequencyMap[pairKey] = pairInfo
				} else {
					// If the pair does not exist, create a new entry
					globalFrequencyMap[pairKey] = PairInfo{
						Frequency: 1,
						BlockIDs:  currentBlockID,
					}
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

				if outputFile != nil {
					outputFile.Close()
				}

				logname := strings.ReplaceAll(inputFile, "/", "")
				// outputPathPrefix := "/mnt/16T/"
				outputFileName := fmt.Sprintf("%srawFreq-%d-%d-Dist%d-%s.log", outputPathPrefix, batchStartIDs[batchIndex], endIDInt, distance, logname)
				outputFile, err = os.Create(outputFileName)
				if err != nil {
					return fmt.Errorf("failed to create output file: %v", err)
				}
				defer outputFile.Close()

				// meet the batch end, dump current map to log file
				for pairKey, pairInfo := range globalFrequencyMap {
					// Only write the log if the frequency is greater than 1
					if pairInfo.Frequency > 1 {
						_, err := outputFile.WriteString(fmt.Sprintf("key: %s; Freq: %d; Blocks: %s\n", pairKey, pairInfo.Frequency, pairInfo.BlockIDs))
						if err != nil {
							return fmt.Errorf("failed to write to output file: %v", err)
						}
					}
				}

				// Print the final block ID in this batch process
				if endBlockID != currentBlockID {
					fmt.Printf("block ID mismatch: start ID %s, end ID %s\n", currentBlockID, endBlockID)
				} else {
					fmt.Printf("The final processed block ID in this batch is %s\n", currentBlockID)
				}

				// Get the current memory usage
				memoryUsage, err := GetMemoryUsage()
				if err != nil {
					fmt.Println("Get memory usage Error:", err)
				}

				fmt.Printf("Current memory usage: %d bytes (%.2f GiB)\n", memoryUsage, float64(memoryUsage)/1024/1024/1024)

				// clear the globalmap
				globalFrequencyMap = make(map[string]PairInfo)

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

// process batches: [20500000, 20759720], [20759721, 21009721], [21009722, 21259722], [21259723, 21500000]
// output log name: endBlockID-rawFreqWithCache-DistX-inputlogname.log
// distance param: 0 1 4 16 64 256 1024
func main() {
	// input log files
	logFiles := []string{
		// "./geth-trace-2025-02-11-19-18-38",
		// "./geth-trace-2025-02-13-15-33-09",
		// "./geth-trace-withcache-merged-block-20500000-21500000",
		"/mnt/ssd/filtered-50K-no-cache-enable-snapshot-with-valuesize",
	}

	outputPathPrefix := "/mnt/ssd/r_correlation/"

	// distanceParams := []int{0, 1, 4, 16, 64, 256, 1024}
	distanceParams := []int{0, 1, 4}

	// batchStartIDs := []int{20500000, 20600000, 20759722, 20884722, 21009722, 21134724, 21259723, 21379862}
	// batchEndIDs := []int{20599999, 20759721, 20884721, 21009721, 21134723, 21259722, 21379861, 21500000}
	batchStartIDs := []int{20500000}
	batchEndIDs := []int{20549999}

	for _, logFile := range logFiles {
		for _, distance := range distanceParams {
			err := ProcessLogBatch(logFile, distance, batchStartIDs, batchEndIDs, outputPathPrefix)
			if err != nil {
				fmt.Println("Error:", err)
			}
		}
	}
}
