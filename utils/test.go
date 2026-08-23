package utils

import (
	"fmt"
	"testing"

	"github.com/cockroachdb/pebble"
)

func TestGet(key []byte, size int, db *pebble.DB) ([]byte, bool) {
	result, err := GetKVSOptDB(db, key)
	if err != nil {
		fmt.Printf("Error getting value from KVS: %v\n", err)
		return result, false
	}
	fmt.Printf("Retrieved value for key %s: %v\n", key, result)
	return result, true
}

func TestPut(key []byte, value []byte, db *pebble.DB) bool {
	err := PutKVSOptDB(db, key, value)
	if err != nil {
		fmt.Printf("Error putting value into KVS: %v\n", err)
		return false
	}
	fmt.Printf("Successfully put value for key %s\n", key)
	return true
}

func TestUpdate(key []byte, value []byte, db *pebble.DB) bool {
	err := UpdateKVSOptDB(db, key, value)

	if err != nil {
		fmt.Printf("Error updating value in KVS: %v\n", err)
		return false
	}

	// Verify the update
	updatedValue, err := GetKVSOptDB(db, key)
	if err != nil {
		fmt.Errorf("GetKVSOptDB after update failed: %v", err)
	}
	if string(updatedValue) != string(value) {
		fmt.Errorf("GetKVSOptDB after update returned incorrect value: got %s, want %s", updatedValue, value)
	}

	fmt.Printf("Successfully updated value for key %s\n", key)
	return true
}

func TestKVSOptDBOperations(t *testing.T) {
	// Load the PebbleDB
	db, err := LoadKVSOptDB()
	if err != nil {
		t.Fatalf("Failed to load PebbleDB: %v", err)
	}
	defer db.Close()

	// Define test key-value pair
	key := []byte("testKey")
	value := []byte("testValue")

	// Test Put operation
	err = PutKVSOptDB(db, key, value)
	if err != nil {
		t.Errorf("PutKVSOptDB failed: %v", err)
	}

	// Test Get operation
	retrievedValue, err := GetKVSOptDB(db, key)
	if err != nil {
		t.Errorf("GetKVSOptDB failed: %v", err)
	}
	if string(retrievedValue) != string(value) {
		t.Errorf("GetKVSOptDB returned incorrect value: got %s, want %s", retrievedValue, value)
	}

	// Test Update operation
	newValue := []byte("updatedValue")
	err = UpdateKVSOptDB(db, key, newValue)
	if err != nil {
		t.Errorf("UpdateKVSOptDB failed: %v", err)
	}

	// Verify the update
	updatedValue, err := GetKVSOptDB(db, key)
	if err != nil {
		t.Errorf("GetKVSOptDB after update failed: %v", err)
	}
	if string(updatedValue) != string(newValue) {
		t.Errorf("GetKVSOptDB after update returned incorrect value: got %s, want %s", updatedValue, newValue)
	}
}

/*
func TestDecoderFunctions(t *testing.T) {
	decoder := &Decoder{}

	// Test DecodeCode
	code := []byte{0x60, 0x0a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}
	decodedCode := decoder.DecodeCode(code)
	if string(decodedCode) != string(code) {
		t.Errorf("DecodeCode returned incorrect value: got %x, want %x", decodedCode, code)
	}

	// Test GenerateTrieNodeStorageKey
	storageRoot := common.HexToHash("0xabc123")
	slotKey := common.HexToHash("0xdef456")
	expectedTrieKey := crypto.Keccak256Hash(storageRoot.Bytes(), slotKey.Bytes())
	generatedTrieKey := decoder.GenerateTrieNodeStorageKey(storageRoot, slotKey)
	if generatedTrieKey != expectedTrieKey {
		t.Errorf("GenerateTrieNodeStorageKey returned incorrect key: got %x, want %x", generatedTrieKey, expectedTrieKey)
	}

	// Test GenerateCodeKey
	codeHash := common.HexToHash("0xdeadbeef")
	generatedCodeKey := decoder.GenerateCodeKey(codeHash)
	if generatedCodeKey != codeHash {
		t.Errorf("GenerateCodeKey returned incorrect key: got %x, want %x", generatedCodeKey, codeHash)
	}
}
*/
