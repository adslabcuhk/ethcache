package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type PairInfo struct {
	Frequency int
	BlockIDs  string // Store block IDs as a single concatenated string
}

const outputPathPrefix = "/mnt/ssd/r_correlation/"

func MergeLogFiles(logFiles []string, outputFile string) error {
	// Global map to store merged frequency and block information for key pairs
	globalMap := make(map[string]PairInfo)

	lineCount := 0

	// Regex to extract key pairs, frequency, and BlockIDs
	re := regexp.MustCompile(`key: ([a-fA-F0-9]+-[0-9]+);([a-fA-F0-9]+-[0-9]+); Freq: (\d+); Blocks: ([0-9;]+)`)

	fmt.Printf("Merge log files\n")

	for _, logFile := range logFiles {
		// Open the log file
		file, err := os.Open(logFile)
		if err != nil {
			return fmt.Errorf("failed to open log file %s: %v", logFile, err)
		}
		defer file.Close()

		// Create a buffered reader
		reader := bufio.NewReader(file)

		// Read the file line by line
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break // End of file reached
				}
				return fmt.Errorf("error reading log file %s: %v", logFile, err)
			}

			line = strings.TrimSpace(line) // Remove any trailing newline or spaces
			lineCount++

			if lineCount%10000 == 0 {
				fmt.Printf("\rMerged %d lines", lineCount)
			}

			// Parse the line using the regex
			matches := re.FindStringSubmatch(line)
			if len(matches) != 5 {
				return fmt.Errorf("invalid log format: %s", line)
			}

			// Extract key pair, frequency, and BlockIDs
			key1 := matches[1]
			key2 := matches[2]
			frequency, err := strconv.Atoi(matches[3])
			if err != nil {
				return fmt.Errorf("failed to parse frequency: %v", err)
			}
			blockIDs := matches[4] // Extract block IDs as a single string

			// Ensure key pairs are order-independent (e.g., key1;key2 is the same as key2;key1)
			keyPair := fmt.Sprintf("%s;%s", key1, key2)
			if key1 > key2 {
				keyPair = fmt.Sprintf("%s;%s", key2, key1)
			}

			// Update the global map
			if existingPairInfo, exists := globalMap[keyPair]; exists {
				// Increment frequency
				existingPairInfo.Frequency += frequency

				// Append block IDs, separated by a semicolon
				existingPairInfo.BlockIDs += ";" + blockIDs

				// Update the map
				globalMap[keyPair] = existingPairInfo
			} else {
				// Create a new entry
				globalMap[keyPair] = PairInfo{
					Frequency: frequency,
					BlockIDs:  blockIDs,
				}
			}
		}
	}

	// Write the merged results to the output file
	output, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer output.Close()

	for keyPair, pairInfo := range globalMap {
		// Write to the output file
		_, err := output.WriteString(fmt.Sprintf("key: %s; Freq: %d; Blocks: %s\n", keyPair, pairInfo.Frequency, pairInfo.BlockIDs))
		if err != nil {
			return fmt.Errorf("failed to write to output file: %v", err)
		}
	}

	fmt.Printf("\nMerged results written to %s\n", outputFile)
	return nil
}

// LogEntry represents a single log entry with its frequency and the original line
type LogEntry struct {
	Frequency int
	Line      string
}

// ParseLogLine extracts the frequency from a log line
func ParseLogLine(line string) (int, error) {
	// Regex to extract the frequency
	re := regexp.MustCompile(`Freq: (\d+)`)
	matches := re.FindStringSubmatch(line)
	if len(matches) < 2 {
		return 0, fmt.Errorf("failed to parse frequency from line: %s", line)
	}

	// Convert frequency to integer
	frequency, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("failed to convert frequency to integer: %v", err)
	}

	return frequency, nil
}

// SortLogFile sorts the log entries by frequency in descending order, writes the sorted results to a log file, and returns the total frequency
func SortLogFile(inputFile, outputFile string) (int, error) {
	// Open the input log file
	file, err := os.Open(inputFile)
	if err != nil {
		return 0, fmt.Errorf("failed to open input file: %v", err)
	}
	defer file.Close()

	// Create a buffered reader
	reader := bufio.NewReader(file)

	// Read the file line by line
	var entries []LogEntry
	var totalFrequency int
	lineCount := 0

	fmt.Printf("Sort merged file\n")

	for {
		// Read until the next newline character
		line, err := reader.ReadString('\n')
		if err != nil {
			// If we reach the end of the file, break the loop
			break
		}

		// Remove the trailing newline character
		line = strings.TrimSuffix(line, "\n")

		lineCount++

		if lineCount%10000 == 0 {
			fmt.Printf("\rProcessed %d lines", lineCount)
		}

		// Parse the frequency from the line
		frequency, err := ParseLogLine(line)
		if err != nil {
			return 0, fmt.Errorf("error parsing log line: %v", err)
		}

		// Accumulate the total frequency
		totalFrequency += frequency

		// Store the entry with its frequency
		entries = append(entries, LogEntry{
			Frequency: frequency,
			Line:      line,
		})
	}

	fmt.Printf("Sorting...\n")

	// Sort the entries by frequency in descending order
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Frequency > entries[j].Frequency
	})

	// Write the sorted entries to the output file
	output, err := os.Create(outputFile)
	if err != nil {
		return 0, fmt.Errorf("failed to create output file: %v", err)
	}
	defer output.Close()

	for _, entry := range entries {
		_, err := output.WriteString(entry.Line + "\n")
		if err != nil {
			return 0, fmt.Errorf("failed to write to output file: %v", err)
		}
	}

	fmt.Printf("Sorted log written to %s\n", outputFile)
	return totalFrequency, nil
}

// PrefixCategory represents a prefix and its corresponding category name
type PrefixCategory struct {
	Prefix   string
	Category string
}

// Define the known prefixes and their categories
var hexPrefixes = []PrefixCategory{
	{"7365637572652d6b65792d", "PreimagePrefix"},
	{"657468657265756d2d636f6e6669672d", "ConfigPrefix"},
	{"657468657265756d2d67656e657369732d", "GenesisPrefix"},
	{"636874526f6f7456322d", "ChtPrefix"},
	{"636874496e64657856322d", "ChtIndexTablePrefix"},
	{"6669786564526f6f742d", "FixedCommitteeRootKey"},
	{"636f6d6d69747465652d", "SyncCommitteeKey"},
	{"6368742d", "ChtTablePrefix"},
	{"626c74526f6f742d", "BloomTriePrefix"},
	{"626c74496e6465782d", "BloomTrieIndexPrefix"},
	{"626c742d", "BloomTrieTablePrefix"},
	{"636c697175652d", "CliqueSnapshotPrefix"},
	{"7570646174652d", "BestUpdateKey"},
	{"536e617073686f7453796e63537461747573", "SnapshotSyncStatusKey"},
	{"536e617073686f7444697361626c6564", "SnapshotDisabledKey"},
	{"536e617073686f74526f6f74", "SnapshotRootKey"},
	{"536e617073686f744a6f75726e616c", "SnapshotJournalKey"},
	{"536e617073686f7447656e657261746f72", "SnapshotGeneratorKey"},
	{"536e617073686f745265636f76657279", "SnapshotRecoveryKey"},
	{"536b656c65746f6e53796e63537461747573", "SkeletonSyncStatusKey"},
	{"5472696553796e63", "FastTrieProgressKey"},
	{"547269654a6f75726e616c", "TrieJournalKey"},
	{"5472616e73616374696f6e496e6465785461696c", "TxIndexTailKey"},
	{"466173745472616e73616374696f6e4c6f6f6b75704c696d6974", "FastTxLookupLimitKey"},
	{"496e76616c6964426c6f636b", "BadBlockKey"},
	{"756e636c65616e2d73687574646f776e", "UncleanShutdownKey"},
	{"657468322d7472616e736974696f6e", "TransitionStatusKey"},
	{"536e617053796e63537461747573", "SnapSyncStatusFlagKey"},
	{"446174616261736556657273696f6e", "DatabaseVersionKey"},
	{"4c617374486561646572", "HeadHeaderKey"},
	{"4c617374426c6f636b", "HeadBlockKey"},
	{"4c61737446617374", "HeadFastBlockKey"},
	{"4c61737446696e616c697a6564", "HeadFinalizedBlockKey"},
	{"4c61737453746174654944", "PersistentStateIDKey"},
	{"4c6173745069766f74", "LastPivotKey"},
	{"69", "BloomBitsIndexPrefix"},
	{"68", "HeaderPrefix"},
	{"74", "HeaderTDSuffix"},
	{"6e", "HeaderHashSuffix"},
	{"48", "HeaderNumberPrefix"},
	{"62", "BlockBodyPrefix"},
	{"72", "BlockReceiptsPrefix"},
	{"6c", "TxLookupPrefix"},
	{"42", "BloomBitsPrefix"},
	{"61", "SnapshotAccountPrefix"},
	{"6f", "SnapshotStoragePrefix"},
	{"63", "CodePrefix"},
	{"53", "SkeletonHeaderPrefix"},
	{"41", "TrieNodeAccountPrefix"},
	{"4f", "TrieNodeStoragePrefix"},
	{"4c", "StateIDPrefix"},
	{"76", "VerklePrefix"},
}

func matchPrefix(key string) string {
	for _, prefix := range hexPrefixes {
		if strings.HasPrefix(key, prefix.Prefix) {
			// fmt.Print("Matched prefix: ", prefix.Prefix, " for key: ", key, "\n")
			return prefix.Category
		}
	}
	return "Unknown"
}

// ParseLineForKeyPairCategories parses a log line containing a key pair and returns the categories of the two keys
func ParseLineForKeyPairCategories(line string) (string, string, bool) {
	// Regex to parse the line format
	re := regexp.MustCompile(`key: ([a-fA-F0-9\-]+);([a-fA-F0-9\-]+); Freq: (\d+); Blocks: ([\d;]+)`)
	matches := re.FindStringSubmatch(line)
	if matches == nil {
		return "", "", false
	}

	// Extract the key pair
	key1 := matches[1]
	key2 := matches[2]

	// Match the prefixes for the two keys and get their categories
	category1 := matchPrefix(key1)
	category2 := matchPrefix(key2)

	return category1, category2, true
}

func GetCategoryFrequency(inputFileName string, outputFileName string, distance int, totalFreq int) error {
	// Map to store the accumulated frequencies for category pairs
	categoryFrequencyMap := make(map[string]int)

	// Map to keep track of open file writers for each category pair
	fileWriters := make(map[string]*os.File)
	defer func() {
		// Ensure all files are closed at the end
		for _, file := range fileWriters {
			file.Close()
		}
	}()

	// Open the input file for reading
	inputFile, err := os.Open(inputFileName)
	if err != nil {
		return fmt.Errorf("failed to open input file: %v", err)
	}
	defer inputFile.Close()

	// Create a buffered reader for reading the file line by line
	reader := bufio.NewReader(inputFile)

	lineCount := 0

	fmt.Printf("Getting category frequency...\n")

	for {
		// Read a single line
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break // End of file reached
			}
			return fmt.Errorf("error reading input file: %v", err)
		}

		line = strings.TrimSpace(line) // Remove any trailing newline or spaces

		lineCount++

		if lineCount%10000 == 0 {
			fmt.Printf("\rProcessed %d lines (by category)", lineCount)
		}

		// Parse the line and extract key pairs and their categories
		category1, category2, ok := ParseLineForKeyPairCategories(line)
		if !ok {
			return fmt.Errorf("failed to parse log line: %s", line)
		}

		// Create a unique key for the category pair (order-independent)
		categoryPair := fmt.Sprintf("%s;%s", category1, category2)
		if category1 > category2 {
			categoryPair = fmt.Sprintf("%s;%s", category2, category1)
		}

		// Extract the frequency value from the line
		re := regexp.MustCompile(`Freq: (\d+);`)
		freqMatch := re.FindStringSubmatch(line)
		if freqMatch == nil {
			return fmt.Errorf("failed to extract frequency from log line: %s", line)
		}

		frequency, err := strconv.Atoi(freqMatch[1])
		if err != nil {
			return fmt.Errorf("failed to convert frequency to integer: %v", err)
		}

		// Update the frequency for the category pair
		categoryFrequencyMap[categoryPair] += frequency

		// Write the original line to the corresponding category pair file
		fileName := fmt.Sprintf("%swithoutcache-Dist%d-%s-%s-freq.log", outputPathPrefix, distance, category1, category2)
		if category1 > category2 {
			fileName = fmt.Sprintf("%swithoutcache-Dist%d-%s-%s-freq.log", outputPathPrefix, distance, category2, category1)
		}

		// Check if the file writer already exists
		writer, exists := fileWriters[fileName]
		if !exists {
			// Create a new file for this category pair
			var err error
			writer, err = os.Create(fileName)
			if err != nil {
				return fmt.Errorf("failed to create log file for category pair %s: %v", categoryPair, err)
			}
			fileWriters[fileName] = writer
		}

		// Write the original line to the file
		_, err = writer.WriteString(line + "\n")
		if err != nil {
			return fmt.Errorf("failed to write to log file for category pair %s: %v", categoryPair, err)
		}
	}

	for fileName, writer := range fileWriters {
		// Close the file writer before checking the file size
		writer.Close()

		// Get the file size of the log for this category
		fileInfo, err := os.Stat(fileName)
		if err != nil {
			return fmt.Errorf("failed to get file info for %s: %v", fileName, err)
		}
		fileSizeGiB := float64(fileInfo.Size()) / (1024 * 1024 * 1024) // Convert bytes to GiB
		fmt.Printf("File size of log for category pair (%s): %.6f GiB\n", fileName, fileSizeGiB)
	}

	// Write the frequency map to the output file, sorted by frequency in descending order
	outputFile, err := os.Create(outputFileName)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer outputFile.Close()

	// Convert the map to a slice of key-value pairs for sorting
	type categoryFrequency struct {
		CategoryPair string
		Frequency    int
	}

	var sortedFrequencies []categoryFrequency
	for categoryPair, frequency := range categoryFrequencyMap {
		sortedFrequencies = append(sortedFrequencies, categoryFrequency{
			CategoryPair: categoryPair,
			Frequency:    frequency,
		})
	}

	// Sort the slice by frequency in descending order
	sort.Slice(sortedFrequencies, func(i, j int) bool {
		return sortedFrequencies[i].Frequency > sortedFrequencies[j].Frequency
	})

	// Write the sorted frequencies to the output file
	for _, entry := range sortedFrequencies {
		_, err := outputFile.WriteString(fmt.Sprintf("%s: %d\n", entry.CategoryPair, entry.Frequency))
		if err != nil {
			return fmt.Errorf("failed to write frequency map to output file: %v", err)
		}
	}

	// write the total frequency
	_, err = outputFile.WriteString(fmt.Sprintf("Total frequency: %d", totalFreq))
	if err != nil {
		return fmt.Errorf("failed to write frequency map to output file: %v", err)
	}

	fmt.Printf("Frequency map written to %s\n", outputFileName)

	return nil
}

func main() {
	// Step 1: Merge log files

	distance := 4

	// List of log files to merge
	// logFiles := []string{
	// 	"./rawFreqWithoutCache-20599999-Dist64-trace-2025-02-11-19-18-38.log",
	// 	"./rawFreqWithoutCache-20759721-Dist64-trace-2025-02-11-19-18-38.log",
	// 	"./rawFreqWithoutCache-20884721-Dist64-trace-2025-02-13-15-33-09.log",
	// 	"./rawFreqWithoutCache-21009721-Dist64-trace-2025-02-13-15-33-09.log",
	// 	"./rawFreqWithoutCache-21134723-Dist64-trace-2025-02-13-15-33-09.log",
	// 	"./rawFreqWithoutCache-21259722-Dist64-trace-2025-02-13-15-33-09.log",
	// 	"./rawFreqWithoutCache-21379861-Dist64-trace-2025-02-13-15-33-09.log",
	// 	"./rawFreqWithoutCache-21500000-Dist64-trace-2025-02-13-15-33-09.log",
	// }
	logFiles := []string{
		"/mnt/ssd/r_correlation/rawFreq-20500000-20549999-Dist4-mntssdfiltered-50K-no-cache-enable-snapshot-with-valuesize.log",
	}

	// Output file to write the merged results
	mergedFile := fmt.Sprintf("%sfreq-merged-%d.log", outputPathPrefix, distance)

	// Merge the log files and write the results to the output file
	err := MergeLogFiles(logFiles, mergedFile)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Setp-2: Do sorting for the merged global log

	// Input and output file paths
	sortedLogFile := fmt.Sprintf("%sfreq-sorted-%d.log", outputPathPrefix, distance)
	categoryFreqFile := fmt.Sprintf("%sfreq-category-%d.log", outputPathPrefix, distance)

	// Sort the log file and get the total frequency
	totalFrequency, err := SortLogFile(mergedFile, sortedLogFile)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Remove the merged log file after sorting
	err = os.Remove(mergedFile)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Printf("Merged log file %s has been removed.\n", mergedFile)

	// Step-3: Print the total frequency
	fmt.Printf("Total frequency: %d\n", totalFrequency)

	// Step-4: Get the category frequency
	err = GetCategoryFrequency(sortedLogFile, categoryFreqFile, distance, totalFrequency)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Print the log file sizes
	fileInfo, err := os.Stat(sortedLogFile)
	if err != nil {
		fmt.Println("failed to get file info for %s: %v", sortedLogFile, err)
		return
	}
	fileSizeGiB := float64(fileInfo.Size()) / (1024 * 1024 * 1024) // Convert bytes to GiB
	fmt.Printf("Total file size of the sorted log: %.6f GiB\n", fileSizeGiB)

}
