package utils

import (
	"encoding/hex"
	"fmt"
	"log"

	"github.com/cockroachdb/pebble"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/crypto"
)

// StorageKVPair represents a key-value pair from the storage trie
type StorageKVPair struct {
	Key   []byte // The full DB key ("O" + accountHash + nodePath)
	Value []byte // The trie node value bytes
}

// GetStorageTrieNodesForAccount retrieves all storage trie node key-value pairs
// for a given account. It:
//
// 1. Decodes the account RLP value to extract StorageRoot and CodeHash
// 2. Computes the accountHash from the account address
// 3. Iterates all trie nodes stored under that account's storage trie
// 4. Returns all matching key-value pairs from the Pebble DB
//
// Parameters:
//   - db: Open Pebble database instance
//   - accountAddr: The account address (20 bytes)
//   - accountRLPValue: The RLP-encoded account value from TrieNodeAccount
//
// Returns:
//   - []StorageKVPair: All storage trie nodes for this account
//   - error: Any error encountered during processing
func GetStorageTrieNodesForAccount(
	db *pebble.DB,
	accountAddr common.Address,
	accountRLPValue []byte,
) ([]StorageKVPair, error) {
	// Step 1: Decode the account RLP value
	decoder := NewDecoder()
	account, err := decoder.DecodeAccount(accountRLPValue)
	if err != nil {
		return nil, fmt.Errorf("failed to decode account: %w", err)
	}

	// Step 2: Compute accountHash = Keccak256(accountAddress)
	accountHash := crypto.Keccak256Hash(accountAddr.Bytes())

	log.Printf("Processing account %s (hash: %s) with StorageRoot: %s",
		accountAddr.Hex(), accountHash.Hex(), account.StorageRoot.Hex())

	// Step 3: Iterate all storage trie nodes for this account in the Pebble DB
	// Keys are prefixed with "O" + accountHash + nodePath
	var result []StorageKVPair

	// Build the DB key prefix for this account's storage trie nodes
	// Format: "O" (1 byte) + accountHash (32 bytes)
	keyPrefix := make([]byte, len(rawdb.TrieNodeStoragePrefix)+common.HashLength)
	copy(keyPrefix, rawdb.TrieNodeStoragePrefix)
	copy(keyPrefix[len(rawdb.TrieNodeStoragePrefix):], accountHash.Bytes())

	// Iterate all keys starting with this prefix
	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: keyPrefix,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	// Iterate and collect all matching keys
	for iter.SeekGE(keyPrefix); iter.Valid(); iter.Next() {
		key := iter.Key()
		value := iter.Value()

		// Check if the key still matches our prefix
		if len(key) < len(keyPrefix) || !matchesAccountPrefix(key, keyPrefix) {
			break // No more keys for this account
		}

		// Copy the key and value (since iterator reuses buffers)
		keyBytes := make([]byte, len(key))
		valBytes := make([]byte, len(value))
		copy(keyBytes, key)
		copy(valBytes, value)

		result = append(result, StorageKVPair{
			Key:   keyBytes,
			Value: valBytes,
		})

		log.Printf("  Found storage node: key=%x, valueSize=%d bytes",
			keyBytes, len(valBytes))
	}

	if err = iter.Error(); err != nil {
		return nil, fmt.Errorf("iterator error: %w", err)
	}

	log.Printf("Total storage trie nodes found for account: %d", len(result))
	return result, nil
}

// matchesAccountPrefix checks if a key matches the account storage prefix
func matchesAccountPrefix(key, prefix []byte) bool {
	if len(key) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if key[i] != prefix[i] {
			return false
		}
	}
	return true
}

// GetStorageTrieNodeByPath retrieves a single storage trie node by its path.
// This is useful when you know a specific nodePath (hex-encoded trie path).
//
// Parameters:
//   - db: Open Pebble database instance
//   - accountAddr: The account address
//   - nodePath: The trie node path (in compact hex form)
//
// Returns:
//   - []byte: The node value bytes
//   - error: Not found or DB error
func GetStorageTrieNodeByPath(
	db *pebble.DB,
	accountAddr common.Address,
	nodePath []byte,
) ([]byte, error) {
	accountHash := crypto.Keccak256Hash(accountAddr.Bytes())

	// Build the full DB key: "O" + accountHash + nodePath
	key := make([]byte, len(rawdb.TrieNodeStoragePrefix)+common.HashLength+len(nodePath))
	n := copy(key, rawdb.TrieNodeStoragePrefix)
	n += copy(key[n:], accountHash.Bytes())
	copy(key[n:], nodePath)

	value, closer, err := db.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to get storage node: %w", err)
	}
	defer closer.Close()

	// Copy value before closing the iterator
	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
}

// PrintStorageKVPairs prints the retrieved storage KV pairs in a readable format
func PrintStorageKVPairs(pairs []StorageKVPair) {
	fmt.Printf("\n=== Storage Trie Key-Value Pairs ===\n")
	fmt.Printf("Total nodes: %d\n", len(pairs))
	for i, pair := range pairs {
		fmt.Printf("\nNode %d:\n", i+1)
		fmt.Printf("  Key (hex): %s\n", hex.EncodeToString(pair.Key))
		fmt.Printf("  Value (hex): %s\n", hex.EncodeToString(pair.Value))
		fmt.Printf("  Key length: %d bytes, Value length: %d bytes\n", len(pair.Key), len(pair.Value))
	}
	fmt.Printf("\n===================================\n")
}

// FilterStorageNodesBySize filters storage nodes by size (useful for analysis)
func FilterStorageNodesBySize(pairs []StorageKVPair, minSize, maxSize int) []StorageKVPair {
	var result []StorageKVPair
	for _, pair := range pairs {
		if len(pair.Value) >= minSize && len(pair.Value) <= maxSize {
			result = append(result, pair)
		}
	}
	return result
}
