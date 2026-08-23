package s3fifo

// Stats captures aggregate counters and occupancy for the S3-FIFO cache.
type Stats struct {
	CapacityBytes int64
	SMaxBytes     int64
	MMaxBytes     int64

	BytesUsed  int64
	SBytes     int64
	MBytes     int64
	GhostBytes int64
	TotalBytes int64

	SEntries int
	MEntries int
	GEntries int

	Gets      int64
	Hits      int64
	Puts      int64
	Evictions int64

	SPromotions  int64
	MReinserts   int64
	GhostInserts int64
}

// HitRate returns the GET hit ratio in [0,1].
func (s Stats) HitRate() float64 {
	if s.Gets == 0 {
		return 0
	}
	return float64(s.Hits) / float64(s.Gets)
}

type queueKind uint8

const (
	queueS queueKind = iota
	queueM
)

type entry struct {
	key   string
	value []byte
	size  int64
	freq  uint8 // capped in [0,3]
	kind  queueKind

	prev *entry
	next *entry
}

type fifoQueue struct {
	head *entry
	tail *entry
	len  int
}

func (q *fifoQueue) pushFront(e *entry) {
	e.prev = nil
	e.next = q.head
	if q.head != nil {
		q.head.prev = e
	} else {
		q.tail = e
	}
	q.head = e
	q.len++
}

func (q *fifoQueue) remove(e *entry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		q.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		q.tail = e.prev
	}
	e.prev = nil
	e.next = nil
	if q.len > 0 {
		q.len--
	}
}

type ghostNode struct {
	key   string
	value []byte
	size  int64
	prev  *ghostNode
	next  *ghostNode
}

type ghostQueue struct {
	head *ghostNode
	tail *ghostNode
	len  int
}

func (q *ghostQueue) pushFront(n *ghostNode) {
	n.prev = nil
	n.next = q.head
	if q.head != nil {
		q.head.prev = n
	} else {
		q.tail = n
	}
	q.head = n
	q.len++
}

func (q *ghostQueue) remove(n *ghostNode) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		q.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		q.tail = n.prev
	}
	n.prev = nil
	n.next = nil
	if q.len > 0 {
		q.len--
	}
}

// Cache implements S3-FIFO with:
// - small FIFO S (10% bytes)
// - main FIFO M (90% bytes)
// - ghost FIFO G (keys+values, bounded by M entry count)
type Cache struct {
	capacityBytes int64
	sMaxBytes     int64
	mMaxBytes     int64

	bytesUsed  int64
	sBytes     int64
	mBytes     int64
	ghostBytes int64

	sQueue fifoQueue
	mQueue fifoQueue
	gQueue ghostQueue

	dataIndex  map[string]*entry
	ghostIndex map[string]*ghostNode

	gets        int64
	hits        int64
	puts        int64
	evictions   int64
	sPromotions int64
	mReinserts  int64
	ghostAdds   int64
}

// NewCache allocates an S3-FIFO cache with the given byte capacity.
func NewCache(byteCapacity int64) *Cache {
	if byteCapacity < 0 {
		byteCapacity = 0
	}

	sMax := byteCapacity / 10
	if byteCapacity > 0 && sMax == 0 {
		sMax = 1
	}
	if sMax > byteCapacity {
		sMax = byteCapacity
	}
	mMax := byteCapacity - sMax

	return &Cache{
		capacityBytes: byteCapacity,
		sMaxBytes:     sMax,
		mMaxBytes:     mMax,
		dataIndex:     make(map[string]*entry),
		ghostIndex:    make(map[string]*ghostNode),
	}
}

// Get returns the cached value for key and increments capped frequency on hit.
func (c *Cache) Get(key string) ([]byte, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	c.gets++
	e, ok := c.dataIndex[key]
	if !ok {
		return nil, false
	}
	c.hits++
	if e.freq < 3 {
		e.freq++
	}
	return e.value, true
}

// Put inserts or updates key/value according to S3-FIFO rules.
func (c *Cache) Put(key string, value []byte) {
	if c == nil || key == "" {
		return
	}
	c.puts++

	size := int64(len(value))
	if c.capacityBytes == 0 || size > c.capacityBytes {
		c.Delete(key)
		c.removeGhostKey(key)
		return
	}

	if existing, ok := c.dataIndex[key]; ok {
		c.updateExisting(existing, value, size)
		return
	}

	if !c.ensureSpace(size, nil) {
		return
	}

	e := &entry{
		key:   key,
		value: append([]byte(nil), value...),
		size:  size,
		freq:  0,
	}

	if _, inGhost := c.ghostIndex[key]; inGhost {
		c.removeGhostKey(key)
		e.kind = queueM
		c.mQueue.pushFront(e)
		c.mBytes += size
	} else {
		e.kind = queueS
		c.sQueue.pushFront(e)
		c.sBytes += size
	}

	c.dataIndex[key] = e
	c.bytesUsed += size
	c.trimGhostToCapacity()
}

// Delete removes a key from cache if present.
func (c *Cache) Delete(key string) bool {
	if c == nil || key == "" {
		return false
	}
	e, ok := c.dataIndex[key]
	if !ok {
		return false
	}
	c.removeDataEntry(e)
	c.trimGhostToCapacity()
	return true
}

// SnapshotStats returns current counters and occupancy.
func (c *Cache) SnapshotStats() Stats {
	if c == nil {
		return Stats{}
	}
	return Stats{
		CapacityBytes: c.capacityBytes,
		SMaxBytes:     c.sMaxBytes,
		MMaxBytes:     c.mMaxBytes,
		BytesUsed:     c.bytesUsed,
		SBytes:        c.sBytes,
		MBytes:        c.mBytes,
		GhostBytes:    c.ghostBytes,
		TotalBytes:    c.bytesUsed + c.ghostBytes,
		SEntries:      c.sQueue.len,
		MEntries:      c.mQueue.len,
		GEntries:      c.gQueue.len,
		Gets:          c.gets,
		Hits:          c.hits,
		Puts:          c.puts,
		Evictions:     c.evictions,
		SPromotions:   c.sPromotions,
		MReinserts:    c.mReinserts,
		GhostInserts:  c.ghostAdds,
	}
}

func (c *Cache) updateExisting(e *entry, value []byte, newSize int64) {
	oldSize := e.size
	e.value = append([]byte(nil), value...)
	e.size = newSize
	delta := newSize - oldSize

	c.bytesUsed += delta
	if e.kind == queueS {
		c.sBytes += delta
	} else {
		c.mBytes += delta
	}

	if c.totalTrackedBytes() <= c.capacityBytes {
		return
	}
	if c.ensureSpace(0, e) {
		return
	}
	// If we could not free enough space without removing the protected object,
	// drop the updated object to preserve capacity safety.
	c.removeDataEntry(e)
}

func (c *Cache) ensureSpace(incoming int64, protected *entry) bool {
	for c.totalTrackedBytes()+incoming > c.capacityBytes {
		if !c.evictOne(protected) {
			return false
		}
	}
	return true
}

func (c *Cache) evictOne(protected *entry) bool {
	if len(c.dataIndex) == 0 {
		return c.evictGhostTail()
	}

	if c.sBytes > c.sMaxBytes {
		if c.evictS(protected) {
			return true
		}
		return c.evictM(protected)
	}

	if c.evictM(protected) {
		return true
	}
	if c.evictS(protected) {
		return true
	}
	return c.evictGhostTail()
}

func (c *Cache) evictS(protected *entry) bool {
	for n := c.sQueue.tail; n != nil; {
		prev := n.prev
		if n == protected {
			n = prev
			continue
		}

		if n.freq > 1 {
			c.sQueue.remove(n)
			c.sBytes -= n.size
			n.kind = queueM
			n.freq = 0
			c.mQueue.pushFront(n)
			c.mBytes += n.size
			c.sPromotions++

			for c.mBytes > c.mMaxBytes {
				if !c.evictM(protected) {
					break
				}
			}
			n = prev
			continue
		}

		key := n.key
		value := append([]byte(nil), n.value...)
		c.removeDataEntry(n)
		c.addGhostKey(key, value)
		c.evictions++
		return true
	}
	return false
}

func (c *Cache) evictM(protected *entry) bool {
	for n := c.mQueue.tail; n != nil; {
		prev := n.prev
		if n == protected {
			n = prev
			continue
		}

		if n.freq > 0 {
			c.mQueue.remove(n)
			c.mBytes -= n.size
			n.freq--
			c.mQueue.pushFront(n)
			c.mBytes += n.size
			c.mReinserts++
			n = prev
			continue
		}

		c.removeDataEntry(n)
		c.evictions++
		return true
	}
	return false
}

func (c *Cache) removeDataEntry(e *entry) {
	if e.kind == queueS {
		c.sQueue.remove(e)
		c.sBytes -= e.size
		if c.sBytes < 0 {
			c.sBytes = 0
		}
	} else {
		c.mQueue.remove(e)
		c.mBytes -= e.size
		if c.mBytes < 0 {
			c.mBytes = 0
		}
	}

	delete(c.dataIndex, e.key)
	c.bytesUsed -= e.size
	if c.bytesUsed < 0 {
		c.bytesUsed = 0
	}
}

func (c *Cache) ghostCapacity() int {
	if c.capacityBytes <= 0 {
		return 0
	}
	if c.mQueue.len <= 0 {
		return 1
	}
	return c.mQueue.len
}

func (c *Cache) addGhostKey(key string, value []byte) {
	if key == "" {
		return
	}
	ghostSize := ghostEntrySizeBytes(key, value)
	if ghostSize <= 0 {
		return
	}

	if existing, ok := c.ghostIndex[key]; ok {
		c.ghostBytes -= existing.size
		if c.ghostBytes < 0 {
			c.ghostBytes = 0
		}
		c.gQueue.remove(existing)
		delete(c.ghostIndex, key)
	}
	if !c.ensureSpace(ghostSize, nil) {
		return
	}

	n := &ghostNode{key: key, value: append([]byte(nil), value...), size: ghostSize}
	c.gQueue.pushFront(n)
	c.ghostIndex[key] = n
	c.ghostBytes += n.size
	c.ghostAdds++
	c.trimGhostToCapacity()
}

func (c *Cache) removeGhostKey(key string) {
	n, ok := c.ghostIndex[key]
	if !ok {
		return
	}
	c.ghostBytes -= n.size
	if c.ghostBytes < 0 {
		c.ghostBytes = 0
	}
	c.gQueue.remove(n)
	delete(c.ghostIndex, key)
}

func (c *Cache) trimGhostToCapacity() {
	capEntries := c.ghostCapacity()
	if capEntries <= 0 {
		for tail := c.gQueue.tail; tail != nil; tail = c.gQueue.tail {
			c.ghostBytes -= tail.size
			if c.ghostBytes < 0 {
				c.ghostBytes = 0
			}
			c.gQueue.remove(tail)
			delete(c.ghostIndex, tail.key)
		}
		return
	}

	for c.gQueue.len > capEntries {
		tail := c.gQueue.tail
		if tail == nil {
			break
		}
		c.ghostBytes -= tail.size
		if c.ghostBytes < 0 {
			c.ghostBytes = 0
		}
		c.gQueue.remove(tail)
		delete(c.ghostIndex, tail.key)
	}
}

func (c *Cache) totalTrackedBytes() int64 {
	return c.bytesUsed + c.ghostBytes
}

func (c *Cache) evictGhostTail() bool {
	tail := c.gQueue.tail
	if tail == nil {
		return false
	}
	c.ghostBytes -= tail.size
	if c.ghostBytes < 0 {
		c.ghostBytes = 0
	}
	c.gQueue.remove(tail)
	delete(c.ghostIndex, tail.key)
	return true
}

func ghostEntrySizeBytes(key string, value []byte) int64 {
	if key == "" {
		return int64(len(value))
	}

	keyBytes := int64(len(key))
	if len(key)%2 == 0 {
		keyBytes = int64(len(key) / 2)
	}

	return keyBytes + int64(len(value))
}
