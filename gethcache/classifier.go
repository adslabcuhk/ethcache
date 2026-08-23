package gethcache

import "sim/utils"

// CacheClass captures the semantic group associated with a key prefix.
type CacheClass string

const (
	CacheClassTrieClean CacheClass = "TrieClean"
	CacheClassHeader    CacheClass = "Header"
	CacheClassBlockBody CacheClass = "BlockBody"
	CacheClassReceipt   CacheClass = "Receipt"
	CacheClassTxLookup  CacheClass = "TxLookup"
	CacheClassCode      CacheClass = "Code"
	CacheClassSnapshot  CacheClass = "Snapshot"
	CacheClassIgnored   CacheClass = "Ignored"
)

var classOrder = []CacheClass{
	CacheClassTrieClean,
	CacheClassHeader,
	CacheClassBlockBody,
	CacheClassReceipt,
	CacheClassTxLookup,
	CacheClassCode,
	CacheClassSnapshot,
}

// classifyBytes converts the raw key to a Go-ethereum category and maps that to a cache class.
func classifyBytes(key []byte) CacheClass {
	category := utils.GetCategoryNameWithBytes(key)
	return classifyCategory(category)
}

// classifyHex expects the key as a hex string without 0x prefix.
func classifyHex(hexKey string) CacheClass {
	category := utils.GetCategoryName(hexKey)
	return classifyCategory(category)
}

func classifyCategory(category string) CacheClass {
	switch category {
	case "TrieNodeAccountPrefix":
		return CacheClassTrieClean
	case "TrieNodeStoragePrefix":
		return CacheClassTrieClean
	case "HeaderPrefix":
		return CacheClassHeader
	case "BlockBodyPrefix":
		return CacheClassBlockBody
	case "BlockReceiptsPrefix":
		return CacheClassReceipt
	case "TxLookupPrefix":
		return CacheClassTxLookup
	case "CodePrefix":
		return CacheClassCode
	case "SnapshotAccountPrefix":
		return CacheClassSnapshot
	case "SnapshotStoragePrefix":
		return CacheClassSnapshot
	default:
		return CacheClassIgnored
	}
}
