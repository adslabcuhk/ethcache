package arc

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestState1HitPromotesToT2(t *testing.T) {
	c := NewCache(64)
	c.Put("k1", []byte("abc"))

	stats := c.SnapshotStats()
	if stats.T1Entries != 1 || stats.T2Entries != 0 {
		t.Fatalf("unexpected list placement after first put: %+v", stats)
	}

	v, ok := c.Get("k1")
	if !ok {
		t.Fatalf("expected hit for k1")
	}
	if string(v) != "abc" {
		t.Fatalf("unexpected value for k1: %q", string(v))
	}

	stats = c.SnapshotStats()
	if stats.T1Entries != 0 || stats.T2Entries != 1 {
		t.Fatalf("expected k1 promoted to T2: %+v", stats)
	}
	if stats.Gets != 1 || stats.Hits != 1 {
		t.Fatalf("unexpected get counters: %+v", stats)
	}
	assertInvariant(t, c)
}

func TestState2B1GhostHitRaisesPAndAdmits(t *testing.T) {
	c := NewCache(10)

	// Build history that creates a B1 ghost entry.
	c.Put("a", []byte("111"))
	c.Put("b", []byte("222"))
	c.Put("c", []byte("333"))

	if _, ok := c.b1Index["a"]; !ok {
		t.Fatalf("expected key a in B1 before B1 ghost hit path")
	}
	if c.targetPBytes != 0 {
		t.Fatalf("expected initial p to be 0, got %d", c.targetPBytes)
	}

	// Ghost hit in B1 should increase p and bring key into T2 with value.
	c.Put("a", []byte("111"))

	stats := c.SnapshotStats()
	if stats.B1Hits != 1 {
		t.Fatalf("expected one B1 ghost hit, got %d", stats.B1Hits)
	}
	if stats.TargetPBytes != 4 {
		t.Fatalf("expected p to increase to 4, got %d", stats.TargetPBytes)
	}
	if _, ok := c.t2Index["a"]; !ok {
		t.Fatalf("expected key a to be in T2 after B1 ghost hit")
	}
	if got := string(c.cacheData["a"]); got != "111" {
		t.Fatalf("unexpected value for key a in cache_data: %q", got)
	}
	assertInvariant(t, c)
}

func TestState3B2GhostHitLowersPAndAdmits(t *testing.T) {
	c := NewCache(12)

	// Seed with three entries.
	c.Put("a", []byte("111"))
	c.Put("b", []byte("222"))
	c.Put("c", []byte("333"))

	// Move one entry to T2 so REPLACE can demote T2->B2.
	if _, ok := c.Get("a"); !ok {
		t.Fatalf("expected hit on a to promote into T2")
	}

	// Force REPLACE to prefer T2 path for the next insertion.
	c.targetPBytes = 10
	c.Put("d", []byte("444"))

	if _, ok := c.b2Index["a"]; !ok {
		t.Fatalf("expected key a in B2 before B2 ghost hit path")
	}

	beforeP := c.targetPBytes
	c.Put("a", []byte("111"))
	stats := c.SnapshotStats()

	if stats.B2Hits != 1 {
		t.Fatalf("expected one B2 ghost hit, got %d", stats.B2Hits)
	}
	if !(stats.TargetPBytes < beforeP) {
		t.Fatalf("expected p to decrease on B2 ghost hit: before=%d after=%d", beforeP, stats.TargetPBytes)
	}
	if stats.TargetPBytes < 0 {
		t.Fatalf("p must stay non-negative, got %d", stats.TargetPBytes)
	}
	if _, ok := c.t2Index["a"]; !ok {
		t.Fatalf("expected key a to be in T2 after B2 ghost hit")
	}
	assertInvariant(t, c)
}

func TestOversizedRequestRejectedAndExistingRemoved(t *testing.T) {
	c := NewCache(8)
	c.Put("x", []byte("12"))

	if _, ok := c.t1Index["x"]; !ok {
		t.Fatalf("expected x in T1 after initial put")
	}

	// key(1) + value(10) = 11 > 8 should reject and remove existing key state.
	c.Put("x", []byte("0123456789"))

	if _, ok := c.t1Index["x"]; ok {
		t.Fatalf("x should be removed from T1 after oversized put")
	}
	if _, ok := c.t2Index["x"]; ok {
		t.Fatalf("x should be removed from T2 after oversized put")
	}
	if _, ok := c.b1Index["x"]; ok {
		t.Fatalf("x should be removed from B1 after oversized put")
	}
	if _, ok := c.b2Index["x"]; ok {
		t.Fatalf("x should be removed from B2 after oversized put")
	}
	if _, ok := c.cacheData["x"]; ok {
		t.Fatalf("x should be removed from cache_data after oversized put")
	}
	assertInvariant(t, c)
}

func TestStressMixedOperationsMaintainsInvariants(t *testing.T) {
	c := NewCache(96)
	rng := rand.New(rand.NewSource(42))
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}

	for i := 0; i < 2000; i++ {
		k := keys[rng.Intn(len(keys))]
		op := rng.Intn(3)

		switch op {
		case 0:
			size := 1 + rng.Intn(24)
			v := make([]byte, size)
			for j := range v {
				v[j] = byte('a' + rng.Intn(26))
			}
			c.Put(k, v)
		case 1:
			_, _ = c.Get(k)
		default:
			_ = c.Delete(k)
		}

		assertInvariant(t, c)
	}
}

func assertInvariant(t *testing.T, c *Cache) {
	t.Helper()

	stats := c.SnapshotStats()
	if stats.TotalBytes != stats.T1Bytes+stats.T2Bytes+stats.B1Bytes+stats.B2Bytes {
		t.Fatalf("total bytes mismatch: %+v", stats)
	}
	if stats.TotalBytes > stats.CapacityBytes {
		t.Fatalf("capacity exceeded: total=%d cap=%d", stats.TotalBytes, stats.CapacityBytes)
	}
	if stats.T1Entries != len(c.t1Index) {
		t.Fatalf("T1 entries mismatch: list=%d index=%d", stats.T1Entries, len(c.t1Index))
	}
	if stats.T2Entries != len(c.t2Index) {
		t.Fatalf("T2 entries mismatch: list=%d index=%d", stats.T2Entries, len(c.t2Index))
	}
	if stats.B1Entries != len(c.b1Index) {
		t.Fatalf("B1 entries mismatch: list=%d index=%d", stats.B1Entries, len(c.b1Index))
	}
	if stats.B2Entries != len(c.b2Index) {
		t.Fatalf("B2 entries mismatch: list=%d index=%d", stats.B2Entries, len(c.b2Index))
	}
	if len(c.cacheData) != len(c.t1Index)+len(c.t2Index) {
		t.Fatalf("cache_data size mismatch: cache_data=%d physical=%d", len(c.cacheData), len(c.t1Index)+len(c.t2Index))
	}

	for k := range c.cacheData {
		if _, ok := c.t1Index[k]; !ok {
			if _, ok := c.t2Index[k]; !ok {
				t.Fatalf("cache_data key %q not present in physical lists", k)
			}
		}
		if _, ok := c.b1Index[k]; ok {
			t.Fatalf("cache_data key %q unexpectedly present in B1", k)
		}
		if _, ok := c.b2Index[k]; ok {
			t.Fatalf("cache_data key %q unexpectedly present in B2", k)
		}
	}

	assertDisjoint(t, "T1", c.t1Index, "T2", c.t2Index)
	assertDisjoint(t, "T1", c.t1Index, "B1", c.b1Index)
	assertDisjoint(t, "T1", c.t1Index, "B2", c.b2Index)
	assertDisjoint(t, "T2", c.t2Index, "B1", c.b1Index)
	assertDisjoint(t, "T2", c.t2Index, "B2", c.b2Index)
	assertDisjoint(t, "B1", c.b1Index, "B2", c.b2Index)
}

func assertDisjoint(t *testing.T, nameA string, a map[string]*node, nameB string, b map[string]*node) {
	t.Helper()
	for k := range a {
		if _, ok := b[k]; ok {
			t.Fatalf("key %s present in both %s and %s", fmt.Sprintf("%q", k), nameA, nameB)
		}
	}
}
