package gethcache

import "testing"

func TestPerClassCacheUsageIncludesKeyValueAndMetadata(t *testing.T) {
	cache := newPerClassCache(perClassCacheConfig{limitKind: cacheLimitBytes, capacityBytes: 1024})
	key := "abcd"
	value := make([]byte, 10)

	cache.put(key, value)

	want := int64(96 + len(key) + len(value))
	if got := cache.currentUsage(); got != want {
		t.Fatalf("unexpected usage: got %d want %d", got, want)
	}
	if got := cache.len(); got != 1 {
		t.Fatalf("unexpected cache len: got %d want 1", got)
	}
}

func TestPerClassCacheUpdateAdjustsUsageDelta(t *testing.T) {
	cache := newPerClassCache(perClassCacheConfig{limitKind: cacheLimitBytes, capacityBytes: 1024})
	key := "k1"

	cache.put(key, make([]byte, 10))
	first := cache.currentUsage()
	cache.put(key, make([]byte, 30))
	second := cache.currentUsage()

	if second <= first {
		t.Fatalf("expected usage to increase after larger update: before=%d after=%d", first, second)
	}

	want := int64(96 + len(key) + 30)
	if second != want {
		t.Fatalf("unexpected usage after update: got %d want %d", second, want)
	}
}

func TestPerClassCacheOversizedUpdateRemovesExisting(t *testing.T) {
	cache := newPerClassCache(perClassCacheConfig{limitKind: cacheLimitBytes, capacityBytes: 120})
	key := "k"

	cache.put(key, make([]byte, 10))
	if _, ok := cache.get(key); !ok {
		t.Fatalf("expected key to exist before oversized update")
	}

	cache.put(key, make([]byte, 30)) // 96 + 1 + 30 > 120
	if _, ok := cache.get(key); ok {
		t.Fatalf("expected oversized update to remove key")
	}
	if got := cache.currentUsage(); got != 0 {
		t.Fatalf("expected zero usage after oversized update, got %d", got)
	}
}

func TestPerClassCacheEvictsUsingFullEntrySize(t *testing.T) {
	cache := newPerClassCache(perClassCacheConfig{limitKind: cacheLimitBytes, capacityBytes: 220})

	cache.put("k1", make([]byte, 20))
	cache.put("k2", make([]byte, 20))

	if _, ok := cache.get("k1"); ok {
		t.Fatalf("expected k1 to be evicted")
	}
	if _, ok := cache.get("k2"); !ok {
		t.Fatalf("expected k2 to remain")
	}
	if got := cache.len(); got != 1 {
		t.Fatalf("unexpected cache len after eviction: got %d want 1", got)
	}
	want := int64(96 + len("k2") + 20)
	if got := cache.currentUsage(); got != want {
		t.Fatalf("unexpected usage after eviction: got %d want %d", got, want)
	}
}

func TestEntryLimitedCacheTracksKVBytesAndWatermark(t *testing.T) {
	cache := newPerClassCache(perClassCacheConfig{limitKind: cacheLimitEntries, capacityEntries: 2})

	cache.put("k1", []byte("ab"))
	cache.put("k2", []byte("abcd"))

	stats := cache.stats()
	if stats.CurrentEntries != 2 {
		t.Fatalf("unexpected current entries: got %d want 2", stats.CurrentEntries)
	}
	wantBytes := int64(len("k1")+len("ab") + len("k2")+len("abcd"))
	if stats.CurrentBytes != wantBytes {
		t.Fatalf("unexpected current bytes: got %d want %d", stats.CurrentBytes, wantBytes)
	}
	if stats.MaxBytes != wantBytes {
		t.Fatalf("unexpected max bytes: got %d want %d", stats.MaxBytes, wantBytes)
	}

	cache.put("k3", []byte("abcdef")) // evicts k1
	stats = cache.stats()
	if stats.CurrentEntries != 2 {
		t.Fatalf("unexpected entries after eviction: got %d want 2", stats.CurrentEntries)
	}
	wantCurrent := int64(len("k2")+len("abcd") + len("k3")+len("abcdef"))
	if stats.CurrentBytes != wantCurrent {
		t.Fatalf("unexpected current bytes after eviction: got %d want %d", stats.CurrentBytes, wantCurrent)
	}
	if stats.MaxBytes < stats.CurrentBytes {
		t.Fatalf("max bytes should be >= current bytes: max=%d current=%d", stats.MaxBytes, stats.CurrentBytes)
	}
}
