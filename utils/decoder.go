package utils

import (
	"encoding/hex"
	"fmt"
	"log"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// Account represents the decoded TrieNodeAccount fields.
type Account struct {
	Nonce       uint64
	Balance     string
	StorageRoot common.Hash
	CodeHash    common.Hash
}

// // SnapshotAccount represents the decoded SnapshotAccount fields.
// type SnapshotAccount struct {
// 	Nonce   uint64
// 	Balance *big.Int
// }

// // SnapshotStorage represents the decoded SnapshotStorage fields.
// type SnapshotStorage struct {
// 	Key   common.Hash
// 	Value common.Hash
// }

// // BlockHeader represents the decoded BlockHeader fields.
// type BlockHeader struct {
// 	Number     uint64
// 	Timestamp  uint64
// 	ParentHash common.Hash
// }

// Decoder is a class-like structure for decoding Ethereum KV pairs.
type Decoder struct{}

// NewDecoder creates a new Decoder instance.
func NewDecoder() *Decoder {
	return &Decoder{}
}

// DecodeAccount decodes the RLP-encoded TrieNodeAccount value.
func (d *Decoder) DecodeAccount(value []byte) (*Account, error) {
	var account types.StateAccount
	err := rlp.DecodeBytes(value, &account)
	if err != nil {
		return nil, err
	}
	return &Account{
		Nonce: account.Nonce,
		Balance: func() string {
			if account.Balance == nil {
				return "0"
			}
			return account.Balance.String()
		}(),
		StorageRoot: account.Root,
		CodeHash:    common.BytesToHash(account.CodeHash),
	}, nil
}

// // DecodeSnapshotAccount decodes the RLP-encoded SnapshotAccount value.
// func (d *Decoder) DecodeSnapshotAccount(value []byte) (*SnapshotAccount, error) {
// 	var snapshotAccount struct {
// 		Nonce   uint64
// 		Balance *big.Int
// 	}
// 	err := rlp.DecodeBytes(value, &snapshotAccount)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &SnapshotAccount{
// 		Nonce:   snapshotAccount.Nonce,
// 		Balance: snapshotAccount.Balance,
// 	}, nil
// }

// // DecodeSnapshotStorage decodes the RLP-encoded SnapshotStorage value.
// func (d *Decoder) DecodeSnapshotStorage(value []byte) (*SnapshotStorage, error) {
// 	var snapshotStorage struct {
// 		Key   common.Hash
// 		Value common.Hash
// 	}
// 	err := rlp.DecodeBytes(value, &snapshotStorage)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &SnapshotStorage{
// 		Key:   snapshotStorage.Key,
// 		Value: snapshotStorage.Value,
// 	}, nil
// }

// // DecodeStorage decodes the RLP-encoded TrieNodeStorage value.
// func (d *Decoder) DecodeStorage(value []byte) ([]byte, error) {
// 	// Storage values are typically raw data or RLP-encoded.
// 	var decoded interface{}
// 	err := rlp.DecodeBytes(value, &decoded)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return value, nil
// }

// // DecodeCode decodes the raw bytecode.
// func (d *Decoder) DecodeCode(value []byte) []byte {
// 	return value // Code is already raw bytecode.
// }

// // DecodeBlockHeader decodes the RLP-encoded BlockHeader value.
// func (d *Decoder) DecodeBlockHeader(value []byte) (*BlockHeader, error) {
// 	var header types.Header
// 	err := rlp.DecodeBytes(value, &header)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &BlockHeader{
// 		Number:     header.Number.Uint64(),
// 		Timestamp:  header.Time,
// 		ParentHash: header.ParentHash,
// 	}, nil
// }

// GenerateTrieNodeStorageKey generates the key for TrieNodeStorage from the storageRoot and slot key.
func (d *Decoder) GenerateTrieNodeStorageKey(storageRoot common.Hash, slotKey common.Hash) common.Hash {
	return crypto.Keccak256Hash(storageRoot.Bytes(), slotKey.Bytes())
}

// GenerateCodeKey generates the key for Code from the codeHash.
func (d *Decoder) GenerateCodeKey(codeHash common.Hash) common.Hash {
	return codeHash // The code key is the codeHash itself.
}

// // GenerateSnapshotStorageKey generates the key for SnapshotStorage from the slot key.
// func (d *Decoder) GenerateSnapshotStorageKey(slotKey common.Hash) common.Hash {
// 	return crypto.Keccak256Hash(slotKey.Bytes())
// }

// // GenerateSnapshotAccountKey generates the key for SnapshotAccount from the account address.
// func (d *Decoder) GenerateSnapshotAccountKey(accountAddress common.Address) common.Hash {
// 	return crypto.Keccak256Hash(accountAddress.Bytes())
// }

// Example usage of the Decoder.
func ExampleUsage() {
	// Example: Read a KV pair from a dump file.
	// Replace these with actual values from your dump file.
	keyHex := "f901..."   // Replace with actual key (hex-encoded)
	valueHex := "f85e..." // Replace with actual value (hex-encoded)

	// Decode the key and value from hex.
	_, err := hex.DecodeString(keyHex)
	if err != nil {
		log.Fatalf("Failed to decode key: %v", err)
	}
	value, err := hex.DecodeString(valueHex)
	if err != nil {
		log.Fatalf("Failed to decode value: %v", err)
	}

	// Create a new decoder instance.
	decoder := NewDecoder()

	// Decode an account and generate related keys.
	account, err := decoder.DecodeAccount(value)
	if err != nil {
		log.Fatalf("Failed to decode account: %v", err)
	}
	fmt.Printf("Decoded Account: %+v\n", account)

	// Generate TrieNodeStorage key.
	slotKey := common.HexToHash("0x1234") // Example slot key.
	storageKey := decoder.GenerateTrieNodeStorageKey(account.StorageRoot, slotKey)
	fmt.Printf("Generated TrieNodeStorage Key: %s\n", storageKey.Hex())

	// Generate Code key.
	codeKey := decoder.GenerateCodeKey(account.CodeHash)
	fmt.Printf("Generated Code Key: %s\n", codeKey.Hex())
}
