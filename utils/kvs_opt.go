package utils

import (
	"log"

	"github.com/cockroachdb/pebble"
)

// input path of pebbleDB (2.05 M)
const db_path string = "/mnt/lvm_data/FAST-26-EthAnalysis/Traces/block-20500000-backup/execution/data/geth/chaindata"

// output path of pebbleDB
const db_path_out string = "/mnt/lvm_data/FAST-26-EthAnalysis/Traces/block-20500000-backup/execution/data/geth/chaindata-kvsopt"

// should be a safe copy of pebbleDB for test

func LoadKVSOptDB() (*pebble.DB, error) {
	db, err := pebble.Open(db_path, &pebble.Options{})
	if err != nil {
		log.Fatalf("Failed to open PebbleDB: %v", err)
		return nil, err
	}
	return db, nil
}

// get value from the pebbleDB
func GetKVSOptDB(db *pebble.DB, key []byte) ([]byte, error) {
	value, closer, err := db.Get(key)
	if err != nil {
		log.Printf("Failed to get key %x: %v", key, err)
		return nil, err
	}
	defer closer.Close()
	return value, nil
}

// put KV pair into the pebbleDB
func PutKVSOptDB(db *pebble.DB, key []byte, value []byte) error {
	err := db.Set(key, value, pebble.NoSync)
	if err != nil {
		log.Printf("Failed to put key %x: %v", key, err)
		return err
	}
	return nil
}

// update KV pair into the pebbleDB
func UpdateKVSOptDB(db *pebble.DB, key []byte, value []byte) error {
	err := db.Set(key, value, pebble.NoSync)
	if err != nil {
		log.Printf("Failed to update key %x: %v", key, err)
		return err
	}
	return nil
}
