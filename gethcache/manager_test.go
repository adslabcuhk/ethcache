package gethcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheManagerAllocations(t *testing.T) {
	metricsFile := filepath.Join(t.TempDir(), "metrics.csv")
	manager, err := NewCacheManager(1<<30, metricsFile)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	expectedBytes := map[CacheClass]int64{
		CacheClassTrieClean: (1 << 30) * 15 / 100,
		CacheClassSnapshot:  (1 << 30) * 10 / 100,
		CacheClassCode:      64 * 1024 * 1024,
	}
	expectedEntries := map[CacheClass]int{
		CacheClassHeader:    512,
		CacheClassBlockBody: 512,
		CacheClassReceipt:   32,
		CacheClassTxLookup:  1024,
	}

	for class, want := range expectedBytes {
		cache, ok := manager.CacheForClass(class)
		if !ok {
			t.Fatalf("missing cache for class %s", class)
		}
		if cache.capacityBytes() != want {
			t.Fatalf("class %s capacity mismatch: got %d want %d", class, cache.capacityBytes(), want)
		}
	}
	for class, want := range expectedEntries {
		cache, ok := manager.CacheForClass(class)
		if !ok {
			t.Fatalf("missing cache for class %s", class)
		}
		if cache.capacityEntries() != want {
			t.Fatalf("class %s entry capacity mismatch: got %d want %d", class, cache.capacityEntries(), want)
		}
	}
}

func TestCacheManagerPutGetAndMetrics(t *testing.T) {
	metricsFile := filepath.Join(t.TempDir(), "metrics.csv")
	manager, err := NewCacheManager(800, metricsFile)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	value := []byte("payload")
	keyHex := "41deadbeef"
	manager.PutHex(keyHex, value)

	cache, ok := manager.CacheForClass(CacheClassTrieClean)
	if !ok {
		t.Fatalf("missing TrieClean cache")
	}
	if cache.len() != 1 {
		t.Fatalf("expected 1 item in TrieClean cache, got %d", cache.len())
	}

	got, found := manager.GetHex(keyHex)
	if !found {
		t.Fatalf("expected cache hit for %s", keyHex)
	}
	if string(got) != string(value) {
		t.Fatalf("unexpected value: %s", string(got))
	}

	if _, found := manager.GetHex("636f6f6c"); found {
		t.Fatalf("expected miss for unknown key")
	}

	if err := manager.FlushMetrics(); err != nil {
		t.Fatalf("flush metrics: %v", err)
	}

	data, err := os.ReadFile(metricsFile)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least one metrics record, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "timestamp") {
		t.Fatalf("expected header row, got %q", lines[0])
	}
}

func TestCacheManagerPutAndPutHexConsistentAccounting(t *testing.T) {
	metricsFile := filepath.Join(t.TempDir(), "metrics.csv")
	manager, err := NewCacheManager(800, metricsFile)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	rawKey := []byte{0x41, 0xde, 0xad, 0xbe, 0xef}
	hexKey := "41deadbeef"
	value := []byte("payload")

	manager.Put(rawKey, value)
	cacheA, ok := manager.CacheForClass(CacheClassTrieClean)
	if !ok {
		t.Fatalf("missing TrieClean cache")
	}
	wantUsage := int64(96 + len(hexKey) + len(value))
	if got := cacheA.currentUsage(); got != wantUsage {
		t.Fatalf("unexpected usage after Put: got %d want %d", got, wantUsage)
	}
	if _, found := manager.GetHex(hexKey); !found {
		t.Fatalf("expected GetHex hit after Put")
	}

	manager2, err := NewCacheManager(800, filepath.Join(t.TempDir(), "metrics2.csv"))
	if err != nil {
		t.Fatalf("new manager2: %v", err)
	}
	manager2.PutHex(hexKey, value)
	cacheB, ok := manager2.CacheForClass(CacheClassTrieClean)
	if !ok {
		t.Fatalf("missing TrieClean cache in manager2")
	}
	if got := cacheB.currentUsage(); got != wantUsage {
		t.Fatalf("unexpected usage after PutHex: got %d want %d", got, wantUsage)
	}
	if _, found := manager2.Get(rawKey); !found {
		t.Fatalf("expected Get hit after PutHex")
	}
}
