package gethcache

import "testing"

func TestClassifyCategoryGethCacheMapping(t *testing.T) {
	tests := map[string]CacheClass{
		"TrieNodeAccountPrefix":  CacheClassTrieClean,
		"TrieNodeStoragePrefix":  CacheClassTrieClean,
		"HeaderPrefix":           CacheClassHeader,
		"BlockBodyPrefix":        CacheClassBlockBody,
		"BlockReceiptsPrefix":    CacheClassReceipt,
		"TxLookupPrefix":         CacheClassTxLookup,
		"CodePrefix":             CacheClassCode,
		"SnapshotAccountPrefix":  CacheClassSnapshot,
		"SnapshotStoragePrefix":  CacheClassSnapshot,
		"SnapshotRootKey":        CacheClassIgnored,
		"HeaderNumberPrefix":     CacheClassIgnored,
		"UnknownPrefixSomething": CacheClassIgnored,
	}
	for category, want := range tests {
		if got := classifyCategory(category); got != want {
			t.Fatalf("category %s mapped to %s, want %s", category, got, want)
		}
	}
}
