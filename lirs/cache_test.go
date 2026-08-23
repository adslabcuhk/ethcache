package lirs

import "testing"

func oneByte(v byte) []byte { return []byte{v} }

func TestWarmupAdmitsLIRThenHIR(t *testing.T) {
	c := NewCache(20)
	for i := byte(0); i < 9; i++ {
		c.Put(string([]byte{'a' + i}), oneByte(i))
	}

	stats := c.SnapshotStats()
	if stats.LIREntries != 9 || stats.HIREntries != 0 || stats.BytesUsed != 18 || stats.KeyBytes != 9 || stats.ValueBytes != 9 {
		t.Fatalf("unexpected warmup stats: %+v", stats)
	}

	c.Put("j", oneByte(9))
	stats = c.SnapshotStats()
	if stats.LIREntries != 9 || stats.HIREntries != 1 || stats.QEntries != 1 || stats.BytesUsed != 20 || stats.KeyBytes != 10 || stats.ValueBytes != 10 {
		t.Fatalf("new block after LIR budget should be resident HIR: %+v", stats)
	}
	if n := c.blockMap["j"]; n == nil || n.status != statusHIR || !n.isResident || !n.inQ {
		t.Fatalf("j should be resident HIR in Q: %#v", n)
	}
}

func TestLIRHitMovesToStackTop(t *testing.T) {
	c := NewCache(20)
	for i := byte(0); i < 9; i++ {
		c.Put(string([]byte{'a' + i}), oneByte(i))
	}
	if c.stack.tail == nil || c.stack.tail.key != "a" {
		t.Fatalf("expected a at stack bottom before hit")
	}

	if _, ok := c.Get("a"); !ok {
		t.Fatalf("expected hit for a")
	}
	if c.stack.head == nil || c.stack.head.key != "a" {
		t.Fatalf("expected a to move to stack top, got head=%#v", c.stack.head)
	}
	stats := c.SnapshotStats()
	if stats.Gets != 1 || stats.Hits != 1 || stats.HitRate() != 1 {
		t.Fatalf("unexpected hit stats: %+v", stats)
	}
}

func TestResidentHIRHitPromotesAndDemotesBottomLIR(t *testing.T) {
	c := NewCache(20)
	for i := byte(0); i < 9; i++ {
		c.Put(string([]byte{'a' + i}), oneByte(i))
	}
	c.Put("j", oneByte(9))

	if _, ok := c.Get("j"); !ok {
		t.Fatalf("expected resident HIR hit")
	}

	j := c.blockMap["j"]
	if j == nil || j.status != statusLIR || !j.isResident || j.inQ || c.stack.head != j {
		t.Fatalf("j should be promoted to resident LIR at stack top: %#v", j)
	}
	a := c.blockMap["a"]
	if a == nil || a.status != statusHIR || !a.isResident || !a.inQ || a.inStack {
		t.Fatalf("bottom LIR a should be demoted to resident HIR in Q and pruned from S: %#v", a)
	}
	stats := c.SnapshotStats()
	if stats.LIREntries != 9 || stats.HIREntries != 1 || stats.QEntries != 1 || stats.Promotions != 1 || stats.Demotions != 1 || stats.Prunes == 0 {
		t.Fatalf("unexpected promotion/demotion stats: %+v", stats)
	}
}

func TestNewHIRMissEvictsQueueHead(t *testing.T) {
	c := NewCache(20)
	for i := byte(0); i < 9; i++ {
		c.Put(string([]byte{'a' + i}), oneByte(i))
	}
	c.Put("j", oneByte(9))

	c.Put("k", oneByte(10))
	j := c.blockMap["j"]
	if j == nil || j.isResident || !j.inStack || j.status != statusHIR {
		t.Fatalf("j should have been evicted from Q but retained as nonresident HIR in Stack S: %#v", j)
	}
	k := c.blockMap["k"]
	if k == nil || k.status != statusHIR || !k.isResident || !k.inQ {
		t.Fatalf("k should be resident HIR in Q: %#v", k)
	}
	stats := c.SnapshotStats()
	if stats.BytesUsed > stats.CapacityBytes || stats.Evictions == 0 || stats.QEntries != 1 {
		t.Fatalf("unexpected eviction stats: %+v", stats)
	}
}

func TestNonResidentHIRRereferencePromotesToLIR(t *testing.T) {
	c := NewCache(20)
	for i := byte(0); i < 9; i++ {
		c.Put(string([]byte{'a' + i}), oneByte(i))
	}
	c.Put("j", oneByte(9))
	c.Put("k", oneByte(10))

	j := c.blockMap["j"]
	if j == nil || j.isResident || !j.inStack || j.status != statusHIR {
		t.Fatalf("j should be a nonresident HIR retained in Stack S: %#v", j)
	}
	if _, ok := c.Get("j"); ok {
		t.Fatalf("nonresident HIR should miss")
	}

	c.Put("j", oneByte(11))
	j = c.blockMap["j"]
	if j == nil || !j.isResident || j.status != statusLIR || c.stack.head != j {
		t.Fatalf("rereferenced j should be promoted to resident LIR at stack top: %#v", j)
	}
	stats := c.SnapshotStats()
	if stats.Promotions == 0 || stats.Demotions == 0 || stats.Evictions < 2 || stats.BytesUsed > stats.CapacityBytes {
		t.Fatalf("unexpected rereference stats: %+v", stats)
	}
}

func TestDeleteAndOversizedValueMaintainCounters(t *testing.T) {
	c := NewCache(4)
	c.Put("a", oneByte(1))
	c.Put("b", oneByte(2))
	if !c.Delete("a") {
		t.Fatalf("expected delete of a")
	}
	if _, ok := c.blockMap["a"]; ok {
		t.Fatalf("a should be removed from block map")
	}
	if stats := c.SnapshotStats(); stats.BytesUsed != 2 || stats.KeyBytes != 1 || stats.ValueBytes != 1 || stats.ResidentEntries != 1 {
		t.Fatalf("unexpected stats after delete: %+v", stats)
	}

	c.Put("b", []byte{1, 2, 3, 4})
	if _, ok := c.blockMap["b"]; ok {
		t.Fatalf("oversized update should delete b")
	}
	if stats := c.SnapshotStats(); stats.BytesUsed != 0 || stats.ResidentEntries != 0 || stats.StackEntries != 0 || stats.QEntries != 0 {
		t.Fatalf("unexpected stats after oversized value: %+v", stats)
	}
}
