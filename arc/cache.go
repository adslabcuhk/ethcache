package arc

import "math"

// Stats captures occupancy and counter values for the ARC cache.
type Stats struct {
	CapacityBytes int64
	TargetPBytes  int64

	SMaxHalfBytes int64
	TotalBytes    int64

	T1Bytes int64
	T2Bytes int64
	B1Bytes int64
	B2Bytes int64

	T1Entries int
	T2Entries int
	B1Entries int
	B2Entries int

	Gets      int64
	Hits      int64
	Puts      int64
	Evictions int64
	B1Hits    int64
	B2Hits    int64
}

// HitRate returns the GET hit ratio in [0,1].
func (s Stats) HitRate() float64 {
	if s.Gets == 0 {
		return 0
	}
	return float64(s.Hits) / float64(s.Gets)
}

type node struct {
	key  string
	prev *node
	next *node
}

type lruList struct {
	head *node
	tail *node
	len  int
}

func (l *lruList) pushFront(n *node) {
	n.prev = nil
	n.next = l.head
	if l.head != nil {
		l.head.prev = n
	} else {
		l.tail = n
	}
	l.head = n
	l.len++
}

func (l *lruList) remove(n *node) {
	if n == nil {
		return
	}
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		l.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		l.tail = n.prev
	}
	n.prev = nil
	n.next = nil
	if l.len > 0 {
		l.len--
	}
}

func (l *lruList) moveToFront(n *node) {
	if n == nil || l.head == n {
		return
	}
	l.remove(n)
	l.pushFront(n)
}

func (l *lruList) popBack() *node {
	if l.tail == nil {
		return nil
	}
	n := l.tail
	l.remove(n)
	return n
}

// Cache implements a byte-tracked ARC policy with physical lists T1/T2 and ghost lists B1/B2.
type Cache struct {
	capacityBytes int64
	targetPBytes  int64

	cacheData map[string][]byte

	t1 lruList
	t2 lruList
	b1 lruList
	b2 lruList

	t1Index map[string]*node
	t2Index map[string]*node
	b1Index map[string]*node
	b2Index map[string]*node

	t1Bytes int64
	t2Bytes int64
	b1Bytes int64
	b2Bytes int64

	gets      int64
	hits      int64
	puts      int64
	evictions int64
	b1Hits    int64
	b2Hits    int64
}

// NewCache allocates an ARC cache with the provided total byte budget.
func NewCache(capacityBytes int64) *Cache {
	if capacityBytes < 0 {
		capacityBytes = 0
	}
	return &Cache{
		capacityBytes: capacityBytes,
		cacheData:     make(map[string][]byte),
		t1Index:       make(map[string]*node),
		t2Index:       make(map[string]*node),
		b1Index:       make(map[string]*node),
		b2Index:       make(map[string]*node),
	}
}

// Get returns a value on physical hit and promotes the key to T2 MRU.
func (c *Cache) Get(key string) ([]byte, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	c.gets++

	if n, ok := c.t1Index[key]; ok {
		value := c.cacheData[key]
		c.t1.remove(n)
		delete(c.t1Index, key)
		c.t1Bytes -= physicalCharge(key, value)
		if c.t1Bytes < 0 {
			c.t1Bytes = 0
		}

		c.t2.pushFront(n)
		c.t2Index[key] = n
		c.t2Bytes += physicalCharge(key, value)
		c.hits++
		return append([]byte(nil), value...), true
	}

	if n, ok := c.t2Index[key]; ok {
		c.t2.moveToFront(n)
		c.hits++
		value := c.cacheData[key]
		return append([]byte(nil), value...), true
	}

	return nil, false
}

// Put applies ARC request handling using key and value size.
func (c *Cache) Put(key string, value []byte) {
	if c == nil || key == "" {
		return
	}
	c.puts++

	if c.capacityBytes == 0 {
		c.removeKeyEverywhere(key)
		return
	}

	reqValue := append([]byte(nil), value...)
	sReq := physicalCharge(key, reqValue)
	if sReq > c.capacityBytes {
		c.removeKeyEverywhere(key)
		return
	}

	// State 1: physical hit in T1/T2.
	if _, ok := c.t1Index[key]; ok {
		c.promoteToT2(key)
		c.updateValueAndRebalance(key, reqValue)
		return
	}
	if n, ok := c.t2Index[key]; ok {
		c.t2.moveToFront(n)
		c.updateValueAndRebalance(key, reqValue)
		return
	}

	// State 2: ghost hit in B1.
	if _, ok := c.b1Index[key]; ok {
		c.b1Hits++
		delta1 := sReq
		if c.b1Bytes < c.b2Bytes && c.b1Bytes > 0 {
			ratio := float64(c.b2Bytes) / float64(c.b1Bytes)
			delta1 = int64(math.Round(float64(sReq) * ratio))
		}
		c.targetPBytes = minInt64(c.targetPBytes+delta1, c.capacityBytes/2)
		c.freeSpace(sReq, key)
		if c.totalBytes()+sReq > c.capacityBytes {
			return
		}
		if !c.moveGhostToT2WithValueFromB1(key, reqValue) {
			c.insertIntoT2(key, reqValue)
		}
		return
	}

	// State 3: ghost hit in B2.
	if _, ok := c.b2Index[key]; ok {
		c.b2Hits++
		delta2 := sReq
		if c.b2Bytes < c.b1Bytes && c.b2Bytes > 0 {
			ratio := float64(c.b1Bytes) / float64(c.b2Bytes)
			delta2 = int64(math.Round(float64(sReq) * ratio))
		}
		c.targetPBytes = maxInt64(c.targetPBytes-delta2, 0)
		c.freeSpace(sReq, key)
		if c.totalBytes()+sReq > c.capacityBytes {
			return
		}
		if !c.moveGhostToT2WithValueFromB2(key, reqValue) {
			c.insertIntoT2(key, reqValue)
		}
		return
	}

	// State 4: complete miss.
	c.freeSpace(sReq, key)
	if c.totalBytes()+sReq > c.capacityBytes {
		return
	}
	c.insertIntoT1(key, reqValue)
}

// Delete removes a key from whichever ARC region it currently occupies.
func (c *Cache) Delete(key string) bool {
	if c == nil || key == "" {
		return false
	}
	return c.removeKeyEverywhere(key)
}

// SnapshotStats returns a point-in-time copy of counters and occupancy.
func (c *Cache) SnapshotStats() Stats {
	if c == nil {
		return Stats{}
	}
	return Stats{
		CapacityBytes: c.capacityBytes,
		TargetPBytes:  c.targetPBytes,
		SMaxHalfBytes: c.capacityBytes / 2,
		TotalBytes:    c.totalBytes(),
		T1Bytes:       c.t1Bytes,
		T2Bytes:       c.t2Bytes,
		B1Bytes:       c.b1Bytes,
		B2Bytes:       c.b2Bytes,
		T1Entries:     c.t1.len,
		T2Entries:     c.t2.len,
		B1Entries:     c.b1.len,
		B2Entries:     c.b2.len,
		Gets:          c.gets,
		Hits:          c.hits,
		Puts:          c.puts,
		Evictions:     c.evictions,
		B1Hits:        c.b1Hits,
		B2Hits:        c.b2Hits,
	}
}

func (c *Cache) freeSpace(sReq int64, reqKey string) {
	if c.capacityBytes == 0 {
		return
	}
	for c.totalBytes()+sReq > c.capacityBytes {
		freed := false
		if c.t1Bytes+c.b1Bytes > c.capacityBytes/2 {
			if c.b1.len > 0 {
				freed = c.dropB1LRU()
			} else {
				freed = c.replace(reqKey)
			}
		} else {
			if c.b2.len > 0 {
				freed = c.dropB2LRU()
			} else {
				freed = c.replace(reqKey)
			}
		}
		if !freed {
			break
		}
	}
}

func (c *Cache) replace(reqKey string) bool {
	inB2 := false
	if _, ok := c.b2Index[reqKey]; ok {
		inB2 = true
	}

	if c.t1.len > 0 && (c.t1Bytes > c.targetPBytes || (inB2 && c.t1Bytes == c.targetPBytes)) {
		victim := c.t1.popBack()
		if victim == nil {
			return false
		}
		delete(c.t1Index, victim.key)

		value := c.cacheData[victim.key]
		delete(c.cacheData, victim.key)

		c.t1Bytes -= physicalCharge(victim.key, value)
		if c.t1Bytes < 0 {
			c.t1Bytes = 0
		}

		c.b1.pushFront(victim)
		c.b1Index[victim.key] = victim
		c.b1Bytes += keyBytes(victim.key)
		c.evictions++
		return true
	}

	victim := c.t2.popBack()
	if victim == nil {
		if c.t1.len > 0 {
			// Fallback for degenerate cases where T2 is empty.
			fallback := c.t1.popBack()
			if fallback == nil {
				return false
			}
			delete(c.t1Index, fallback.key)
			value := c.cacheData[fallback.key]
			delete(c.cacheData, fallback.key)
			c.t1Bytes -= physicalCharge(fallback.key, value)
			if c.t1Bytes < 0 {
				c.t1Bytes = 0
			}
			c.b1.pushFront(fallback)
			c.b1Index[fallback.key] = fallback
			c.b1Bytes += keyBytes(fallback.key)
			c.evictions++
			return true
		}
		return false
	}
	delete(c.t2Index, victim.key)

	value := c.cacheData[victim.key]
	delete(c.cacheData, victim.key)
	c.t2Bytes -= physicalCharge(victim.key, value)
	if c.t2Bytes < 0 {
		c.t2Bytes = 0
	}

	c.b2.pushFront(victim)
	c.b2Index[victim.key] = victim
	c.b2Bytes += keyBytes(victim.key)
	c.evictions++
	return true
}

func (c *Cache) promoteToT2(key string) {
	n, ok := c.t1Index[key]
	if !ok {
		return
	}
	value := c.cacheData[key]
	c.t1.remove(n)
	delete(c.t1Index, key)
	c.t1Bytes -= physicalCharge(key, value)
	if c.t1Bytes < 0 {
		c.t1Bytes = 0
	}

	c.t2.pushFront(n)
	c.t2Index[key] = n
	c.t2Bytes += physicalCharge(key, value)
}

func (c *Cache) updateValueAndRebalance(key string, value []byte) {
	oldValue := c.cacheData[key]
	oldValBytes := valueBytes(oldValue)
	newValBytes := valueBytes(value)
	delta := newValBytes - oldValBytes
	if delta > 0 {
		c.freeSpace(delta, key)
	}

	// Apply update if we can still fit globally.
	if c.totalBytes()+delta > c.capacityBytes {
		return
	}

	if _, ok := c.t1Index[key]; ok {
		c.t1Bytes += delta
		if c.t1Bytes < 0 {
			c.t1Bytes = 0
		}
	} else if _, ok := c.t2Index[key]; ok {
		c.t2Bytes += delta
		if c.t2Bytes < 0 {
			c.t2Bytes = 0
		}
	}
	c.cacheData[key] = append([]byte(nil), value...)
}

func (c *Cache) moveGhostToT2WithValueFromB1(key string, value []byte) bool {
	n, ok := c.b1Index[key]
	if !ok {
		return false
	}
	c.b1.remove(n)
	delete(c.b1Index, key)
	c.b1Bytes -= keyBytes(key)
	if c.b1Bytes < 0 {
		c.b1Bytes = 0
	}

	c.cacheData[key] = append([]byte(nil), value...)
	c.t2.pushFront(n)
	c.t2Index[key] = n
	c.t2Bytes += physicalCharge(key, value)
	return true
}

func (c *Cache) moveGhostToT2WithValueFromB2(key string, value []byte) bool {
	n, ok := c.b2Index[key]
	if !ok {
		return false
	}
	c.b2.remove(n)
	delete(c.b2Index, key)
	c.b2Bytes -= keyBytes(key)
	if c.b2Bytes < 0 {
		c.b2Bytes = 0
	}

	c.cacheData[key] = append([]byte(nil), value...)
	c.t2.pushFront(n)
	c.t2Index[key] = n
	c.t2Bytes += physicalCharge(key, value)
	return true
}

func (c *Cache) insertIntoT1(key string, value []byte) {
	if _, exists := c.t1Index[key]; exists {
		return
	}
	if _, exists := c.t2Index[key]; exists {
		return
	}
	n := &node{key: key}
	c.cacheData[key] = append([]byte(nil), value...)
	c.t1.pushFront(n)
	c.t1Index[key] = n
	c.t1Bytes += physicalCharge(key, value)
}

func (c *Cache) insertIntoT2(key string, value []byte) {
	if _, exists := c.t2Index[key]; exists {
		return
	}
	if _, exists := c.t1Index[key]; exists {
		return
	}
	if n, exists := c.b1Index[key]; exists {
		c.b1.remove(n)
		delete(c.b1Index, key)
		c.b1Bytes -= keyBytes(key)
		if c.b1Bytes < 0 {
			c.b1Bytes = 0
		}
	}
	if n, exists := c.b2Index[key]; exists {
		c.b2.remove(n)
		delete(c.b2Index, key)
		c.b2Bytes -= keyBytes(key)
		if c.b2Bytes < 0 {
			c.b2Bytes = 0
		}
	}
	n := &node{key: key}
	c.cacheData[key] = append([]byte(nil), value...)
	c.t2.pushFront(n)
	c.t2Index[key] = n
	c.t2Bytes += physicalCharge(key, value)
}

func (c *Cache) dropB1LRU() bool {
	n := c.b1.popBack()
	if n == nil {
		return false
	}
	delete(c.b1Index, n.key)
	c.b1Bytes -= keyBytes(n.key)
	if c.b1Bytes < 0 {
		c.b1Bytes = 0
	}
	return true
}

func (c *Cache) dropB2LRU() bool {
	n := c.b2.popBack()
	if n == nil {
		return false
	}
	delete(c.b2Index, n.key)
	c.b2Bytes -= keyBytes(n.key)
	if c.b2Bytes < 0 {
		c.b2Bytes = 0
	}
	return true
}

func (c *Cache) removeKeyEverywhere(key string) bool {
	removed := false

	if n, ok := c.t1Index[key]; ok {
		value := c.cacheData[key]
		c.t1.remove(n)
		delete(c.t1Index, key)
		c.t1Bytes -= physicalCharge(key, value)
		if c.t1Bytes < 0 {
			c.t1Bytes = 0
		}
		delete(c.cacheData, key)
		removed = true
	}
	if n, ok := c.t2Index[key]; ok {
		value, okData := c.cacheData[key]
		if !okData {
			value = nil
		}
		c.t2.remove(n)
		delete(c.t2Index, key)
		c.t2Bytes -= physicalCharge(key, value)
		if c.t2Bytes < 0 {
			c.t2Bytes = 0
		}
		delete(c.cacheData, key)
		removed = true
	}
	if n, ok := c.b1Index[key]; ok {
		c.b1.remove(n)
		delete(c.b1Index, key)
		c.b1Bytes -= keyBytes(key)
		if c.b1Bytes < 0 {
			c.b1Bytes = 0
		}
		removed = true
	}
	if n, ok := c.b2Index[key]; ok {
		c.b2.remove(n)
		delete(c.b2Index, key)
		c.b2Bytes -= keyBytes(key)
		if c.b2Bytes < 0 {
			c.b2Bytes = 0
		}
		removed = true
	}

	if !removed {
		delete(c.cacheData, key)
	}
	return removed
}

func (c *Cache) totalBytes() int64 {
	total := c.t1Bytes + c.t2Bytes + c.b1Bytes + c.b2Bytes
	if total < 0 {
		return 0
	}
	return total
}

func keyBytes(key string) int64 {
	return int64(len(key))
}

func valueBytes(value []byte) int64 {
	return int64(len(value))
}

func physicalCharge(key string, value []byte) int64 {
	return keyBytes(key) + valueBytes(value)
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
