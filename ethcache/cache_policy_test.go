package ethcache

import "testing"

func TestByteCapacityRespected(t *testing.T) {
	cache := NewCache[string, []byte](400, nil)

	cache.Put("a", make([]byte, 120))
	cache.Put("b", make([]byte, 120))
	cache.Put("c", make([]byte, 120))

	if cache.BytesUsed() > cache.ByteCapacity() {
		t.Fatalf("bytes used exceeds capacity: got %d cap %d", cache.BytesUsed(), cache.ByteCapacity())
	}
	if cache.Len() == 0 {
		t.Fatalf("expected at least one item in cache")
	}
}

func TestOversizedUpdateRejected(t *testing.T) {
	cache := NewCache[string, []byte](300, nil)

	cache.Put("hot", make([]byte, 32))
	if _, ok := cache.Get("hot"); !ok {
		t.Fatalf("expected hot key to be cached")
	}

	cache.Put("hot", make([]byte, 1024))
	if _, ok := cache.Get("hot"); ok {
		t.Fatalf("expected oversized value update to be rejected")
	}
	if cache.BytesUsed() > cache.ByteCapacity() {
		t.Fatalf("bytes used exceeds capacity: got %d cap %d", cache.BytesUsed(), cache.ByteCapacity())
	}
}

func TestDeleteReducesBytesUsed(t *testing.T) {
	cache := NewCache[string, []byte](600, nil)
	cache.Put("k1", make([]byte, 64))
	cache.Put("k2", make([]byte, 64))

	before := cache.BytesUsed()
	if before == 0 {
		t.Fatalf("expected bytes used to be non-zero after inserts")
	}

	if !cache.Delete("k1") {
		t.Fatalf("expected delete to return true")
	}
	if cache.BytesUsed() >= before {
		t.Fatalf("expected bytes used to decrease after delete: before=%d after=%d", before, cache.BytesUsed())
	}
}

func TestGlobalTickIncrementsOnEveryGet(t *testing.T) {
	cache := NewCache[string, []byte](1024, []int{1, 3})
	cache.Put("a", []byte("1"))

	if got := cache.globalTick.Load(); got != 0 {
		t.Fatalf("expected initial tick 0, got %d", got)
	}

	if _, ok := cache.Get("missing"); ok {
		t.Fatalf("missing key unexpectedly hit")
	}
	if got := cache.globalTick.Load(); got != 1 {
		t.Fatalf("expected tick 1 after miss GET, got %d", got)
	}

	if _, ok := cache.Get("a"); !ok {
		t.Fatalf("expected key a hit")
	}
	if got := cache.globalTick.Load(); got != 2 {
		t.Fatalf("expected tick 2 after hit GET, got %d", got)
	}

	entry := cache.index["a"]
	if entry == nil {
		t.Fatalf("entry a missing")
	}
	if entry.lastAccessedTick != 2 {
		t.Fatalf("expected lastAccessedTick=2, got %d", entry.lastAccessedTick)
	}
}

func TestLevelOneTailExpiredDemotedOnGetLoop(t *testing.T) {
	cache := NewCache[string, []byte](4096, []int{1, 3})
	cache.SetLevel1TTL(2)

	cache.Put("a", []byte("A"))
	cache.Put("b", []byte("B"))

	// Promote both keys into level 1 (thresholds 1,3 => level1 starts at freq=3).
	cache.Get("a")
	cache.Get("a")
	cache.Get("b")
	cache.Get("b")

	level0 := cache.bands[0].level
	level1 := cache.bands[1].level
	if level1.tail == nil {
		t.Fatalf("expected level1 to have entries before TTL checks")
	}

	// Advance time with misses so both level1 entries become expired.
	cache.Get("m1")
	cache.Get("m2")
	cache.Get("m3")

	// This GET must run the demotion loop and clear all expired level1 tail entries.
	cache.Get("m4")

	if level1.tail != nil {
		t.Fatalf("expected level1 empty after demotion loop")
	}

	if level0.head == nil || level0.head.next == nil {
		t.Fatalf("expected demoted entries at level0 head")
	}

	for _, key := range []string{"a", "b"} {
		entry := cache.index[key]
		if entry == nil {
			t.Fatalf("entry %s missing", key)
		}
		if entry.block == nil || entry.block.level != level0 {
			t.Fatalf("entry %s expected in level0 after demotion", key)
		}
		if entry.block.freq != 2 {
			t.Fatalf("entry %s expected freq reset to level0 max boundary 2, got %d", key, entry.block.freq)
		}
	}
}
