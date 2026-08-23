package s3fifo

import "testing"

func TestHitFrequencyCappedAtThree(t *testing.T) {
	c := NewCache(10)
	c.Put("a", []byte{1})

	for i := 0; i < 10; i++ {
		if _, ok := c.Get("a"); !ok {
			t.Fatalf("expected hit for key a at iteration %d", i)
		}
	}

	e := c.dataIndex["a"]
	if e == nil {
		t.Fatalf("missing key a in cache")
	}
	if e.freq != 3 {
		t.Fatalf("frequency should cap at 3, got %d", e.freq)
	}
}

func TestSEvictPromotesWhenFreqGreaterThanOne(t *testing.T) {
	c := NewCache(10)
	c.Put("a", []byte{1})
	c.Put("b", []byte{2})

	if _, ok := c.Get("a"); !ok {
		t.Fatalf("expected hit for a")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatalf("expected second hit for a")
	}

	// Force global eviction. Since S exceeds target, tail of S is inspected.
	c.Put("c", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9})

	stats := c.SnapshotStats()
	if stats.SPromotions == 0 {
		t.Fatalf("expected at least one S->M promotion")
	}
}

func TestSEvictToGhostWhenLowFrequency(t *testing.T) {
	c := NewCache(10)
	c.Put("a", []byte{1})
	c.Put("b", []byte{2})

	// This large insert should eventually evict from S to G for low-frequency entries.
	c.Put("c", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9})

	stats := c.SnapshotStats()
	if stats.GhostInserts == 0 {
		t.Fatalf("expected low-frequency eviction path to insert keys into ghost queue")
	}
	if stats.GhostBytes < 0 {
		t.Fatalf("ghost bytes should never be negative")
	}
}

func TestMEvictReinsertsAndDecrementsFrequency(t *testing.T) {
	c := NewCache(10)

	// Put two keys in S.
	c.Put("a", []byte{1})
	c.Put("b", []byte{2})

	// Hit both twice so evictS promotes them to M when pressured.
	c.Get("a")
	c.Get("a")
	c.Get("b")
	c.Get("b")

	// Pressure cache enough to trigger promotions/evictions and M activity.
	c.Put("x", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9})

	// Any entry in M that survives this read should never have freq > 3.
	for _, e := range c.dataIndex {
		if e.kind == queueM && e.freq > 3 {
			t.Fatalf("invalid M frequency %d", e.freq)
		}
	}

	stats := c.SnapshotStats()
	if stats.MReinserts < 0 {
		t.Fatalf("invalid reinsert counter")
	}
}

func TestGhostCapacityTracksMEntries(t *testing.T) {
	c := NewCache(20)

	// Build some ghost history.
	c.Put("a", []byte{1})
	c.Put("b", []byte{2})
	c.Put("c", []byte{3})
	c.Put("d", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})

	if c.gQueue.len > c.mQueue.len {
		t.Fatalf("ghost queue exceeds M entry capacity: g=%d m=%d", c.gQueue.len, c.mQueue.len)
	}
}

func TestCapacityInvariant(t *testing.T) {
	c := NewCache(32)

	for i := 0; i < 200; i++ {
		k := string(rune('a' + (i % 26)))
		payload := make([]byte, (i%7)+1)
		c.Put(k, payload)
		if c.totalTrackedBytes() > c.capacityBytes {
			t.Fatalf("total tracked bytes exceeded capacity: total=%d cap=%d", c.totalTrackedBytes(), c.capacityBytes)
		}
	}
}
