package utils

import (
	"fmt"
	"strings"
)

// PrefixCategory represents a prefix and its corresponding category name
type PrefixCategory struct {
	Prefix   string
	Category string
}

func matchPrefix(key string) string {
	for _, prefix := range hexPrefixes {
		if strings.HasPrefix(key, prefix.Prefix) {
			// fmt.Printf("Debug: Matched prefix: %s, %s for key: %s\n", prefix.Prefix, prefix.Category, key)
			return prefix.Category
		}
	}
	// fmt.Printf("Debug: No prefix matched for key: %s\n", key)
	return "Unknown"
}

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

func GetCategoryName(key string) string {
	category := matchPrefix(key)
	return category
}

func GetCategoryNameWithBytes(key []byte) string {
	hexKey := fmt.Sprintf("%x", key)
	return GetCategoryName(hexKey)
}

// // GetCategoryName exposes the category lookup for external packages.
// func GetCategoryName(key string) string {
// 	return GetCategoryName(key)
// }
