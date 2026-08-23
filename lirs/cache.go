package lirs

import "strings"

// Stats captures aggregate counters and occupancy for the LIRS cache.
type Stats struct {
	CapacityBytes int64
	LIRMaxBytes   int64
	HIRMaxBytes   int64
	MaxStackBytes int64

	BytesUsed         int64
	KeyBytes          int64
	ValueBytes        int64
	LIRBytes          int64
	HIRBytes          int64
	StackTrackedBytes int64

	BlockMapEntries int
	StackEntries    int
	QEntries        int
	ResidentEntries int
	LIREntries      int
	HIREntries      int

	Gets      int64
	Hits      int64
	Puts      int64
	Evictions int64

	Promotions int64
	Demotions  int64
	Prunes     int64
	Deletes    int64
}

// HitRate returns the GET hit ratio in [0,1].
func (s Stats) HitRate() float64 {
	if s.Gets == 0 {
		return 0
	}
	return float64(s.Hits) / float64(s.Gets)
}

type blockStatus uint8

const (
	statusLIR blockStatus = iota
	statusHIR
)

type node struct {
	key        string
	keySize    int64
	value      []byte
	size       int64
	status     blockStatus
	isResident bool

	inStack bool
	sPrev   *node
	sNext   *node

	inQ   bool
	qPrev *node
	qNext *node
}

type stackList struct {
	head *node // top/MRU
	tail *node // bottom/LRU
	len  int
}

type hirQueue struct {
	head *node // eviction front/FIFO oldest
	tail *node // enqueue end/FIFO newest
	len  int
}

// Cache implements the LIRS replacement policy with Stack S and resident HIR Queue Q.
type Cache struct {
	capacityBytes int64
	lirMaxBytes   int64
	hirMaxBytes   int64
	maxStackBytes int64

	bytesUsed         int64
	keyBytes          int64
	valueBytes        int64
	lirBytes          int64
	hirBytes          int64
	stackTrackedBytes int64
	residentEntries   int
	lirEntries        int
	hirEntries        int

	blockMap map[string]*node
	stack    stackList
	hirQ     hirQueue

	gets       int64
	hits       int64
	puts       int64
	evictions  int64
	promotions int64
	demotions  int64
	prunes     int64
	deletes    int64
}

// NewCache allocates a LIRS cache with the given byte capacity. The LIR/HIR
// split follows the paper's 99%/1% recommendation as soft byte budgets so that
// this cache can be driven by the existing byte-oriented trace framework.
func NewCache(byteCapacity int64) *Cache {
	if byteCapacity < 0 {
		byteCapacity = 0
	}

	hirMax := byteCapacity / 100
	if byteCapacity > 0 && hirMax == 0 {
		hirMax = 1
	}
	if hirMax > byteCapacity {
		hirMax = byteCapacity
	}
	lirMax := byteCapacity - hirMax

	maxStack := int64(0)
	if byteCapacity > 0 {
		maxStack = 2 * byteCapacity
	}

	return &Cache{
		capacityBytes: byteCapacity,
		lirMaxBytes:   lirMax,
		hirMaxBytes:   hirMax,
		maxStackBytes: maxStack,
		blockMap:      make(map[string]*node),
	}
}

// Get returns the resident cached value for key and applies LIRS hit handling.
func (c *Cache) Get(key string) ([]byte, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	c.gets++

	n, ok := c.blockMap[key]
	if !ok || !n.isResident {
		return nil, false
	}

	c.hits++
	value := append([]byte(nil), n.value...)
	c.accessResident(n)
	return value, true
}

// Put inserts or updates key/value and applies LIRS miss/load handling when the
// key is not currently resident.
func (c *Cache) Put(key string, value []byte) {
	if c == nil || key == "" {
		return
	}
	c.puts++

	size := int64(len(value))
	if c.capacityBytes == 0 || keySize(key)+size > c.capacityBytes {
		c.Delete(key)
		return
	}

	if existing, ok := c.blockMap[key]; ok && existing.isResident {
		c.updateResident(existing, value, size)
		return
	}

	c.loadMiss(key, value, size)
}

// Delete removes key from the cache and from any LIRS metadata lists.
func (c *Cache) Delete(key string) bool {
	if c == nil || key == "" {
		return false
	}
	n, ok := c.blockMap[key]
	if !ok {
		return false
	}

	if n.inQ {
		c.removeQ(n)
	}
	if n.inStack {
		c.removeStack(n)
	}
	if n.isResident {
		charge := residentCharge(n)
		c.bytesUsed -= charge
		c.keyBytes -= n.keySize
		c.valueBytes -= n.size
		if c.bytesUsed < 0 {
			c.bytesUsed = 0
		}
		if c.keyBytes < 0 {
			c.keyBytes = 0
		}
		if c.valueBytes < 0 {
			c.valueBytes = 0
		}
		c.residentEntries--
		if c.residentEntries < 0 {
			c.residentEntries = 0
		}
		if n.status == statusLIR {
			c.lirBytes -= charge
			c.lirEntries--
			if c.lirBytes < 0 {
				c.lirBytes = 0
			}
			if c.lirEntries < 0 {
				c.lirEntries = 0
			}
		} else {
			c.hirBytes -= charge
			c.hirEntries--
			if c.hirBytes < 0 {
				c.hirBytes = 0
			}
			if c.hirEntries < 0 {
				c.hirEntries = 0
			}
		}
		if n.status == statusLIR {
			c.stackPruning()
		}
	} else {
		c.bytesUsed -= n.keySize
		c.keyBytes -= n.keySize
		if c.bytesUsed < 0 {
			c.bytesUsed = 0
		}
		if c.keyBytes < 0 {
			c.keyBytes = 0
		}
	}

	delete(c.blockMap, key)
	c.deletes++
	return true
}

// SnapshotStats returns a point-in-time copy of LIRS occupancy and counters.
func (c *Cache) SnapshotStats() Stats {
	if c == nil {
		return Stats{}
	}
	return Stats{
		CapacityBytes:     c.capacityBytes,
		LIRMaxBytes:       c.lirMaxBytes,
		HIRMaxBytes:       c.hirMaxBytes,
		MaxStackBytes:     c.maxStackBytes,
		BytesUsed:         c.bytesUsed,
		KeyBytes:          c.keyBytes,
		ValueBytes:        c.valueBytes,
		LIRBytes:          c.lirBytes,
		HIRBytes:          c.hirBytes,
		StackTrackedBytes: c.stackTrackedBytes,
		BlockMapEntries:   len(c.blockMap),
		StackEntries:      c.stack.len,
		QEntries:          c.hirQ.len,
		ResidentEntries:   c.residentEntries,
		LIREntries:        c.lirEntries,
		HIREntries:        c.hirEntries,
		Gets:              c.gets,
		Hits:              c.hits,
		Puts:              c.puts,
		Evictions:         c.evictions,
		Promotions:        c.promotions,
		Demotions:         c.demotions,
		Prunes:            c.prunes,
		Deletes:           c.deletes,
	}
}

func (c *Cache) accessResident(n *node) {
	if n.status == statusLIR {
		wasBottom := c.stack.tail == n
		c.moveStackTop(n)
		if wasBottom {
			c.stackPruning()
		}
		return
	}

	wasInStack := n.inStack
	c.moveStackTop(n)
	if wasInStack {
		c.promoteHIRToLIR(n)
		c.removeQ(n)
		c.demoteBottomLIR(n)
		c.stackPruning()
		return
	}

	c.moveQTail(n)
}

func (c *Cache) updateResident(n *node, value []byte, size int64) {
	oldSize := n.size
	delta := size - oldSize
	if n.keySize+size > c.capacityBytes {
		c.Delete(n.key)
		return
	}

	n.value = append([]byte(nil), value...)
	n.size = size
	c.bytesUsed += delta
	c.valueBytes += delta
	if n.inStack {
		c.stackTrackedBytes += delta
		if c.stackTrackedBytes < 0 {
			c.stackTrackedBytes = 0
		}
	}
	if n.status == statusLIR {
		c.lirBytes += delta
		if c.lirBytes < 0 {
			c.lirBytes = 0
		}
	} else {
		c.hirBytes += delta
		if c.hirBytes < 0 {
			c.hirBytes = 0
		}
	}
	if c.bytesUsed < 0 {
		c.bytesUsed = 0
	}
	if c.valueBytes < 0 {
		c.valueBytes = 0
	}

	if !c.ensureSpaceFor(0, n) || c.bytesUsed > c.capacityBytes {
		c.Delete(n.key)
		return
	}
	c.stackPruning()
}

func (c *Cache) loadMiss(key string, value []byte, size int64) {
	n, existed := c.blockMap[key]
	wasInStack := existed && n.inStack
	kSize := keySize(key)
	newCharge := kSize + size
	if newCharge > c.capacityBytes {
		if existed {
			c.Delete(key)
		}
		return
	}
	additionalCharge := newCharge
	if existed {
		additionalCharge = newCharge - nodeCharge(n)
	}
	if !existed {
		n = &node{key: key, keySize: kSize, status: statusHIR}
	}

	admitAsLIR := !wasInStack && c.shouldWarmupAsLIR(newCharge)
	if !admitAsLIR {
		if !c.ensureSpaceFor(additionalCharge, n) {
			return
		}
	}
	if !existed {
		c.blockMap[key] = n
	}

	if n.inStack {
		c.stackTrackedBytes += additionalCharge
		if c.stackTrackedBytes < 0 {
			c.stackTrackedBytes = 0
		}
	}
	n.keySize = kSize
	n.size = size
	n.value = append([]byte(nil), value...)

	n.isResident = true
	c.bytesUsed += additionalCharge
	if !existed {
		c.keyBytes += kSize
	}
	c.valueBytes += size
	c.residentEntries++
	c.moveStackTop(n)

	if admitAsLIR {
		n.status = statusLIR
		c.lirBytes += residentCharge(n)
		c.lirEntries++
		c.enforceStackLimit()
		return
	}

	if wasInStack {
		n.status = statusHIR
		c.hirBytes += residentCharge(n)
		c.hirEntries++
		c.promoteHIRToLIR(n)
		c.demoteBottomLIR(n)
		c.stackPruning()
		return
	}

	n.status = statusHIR
	c.hirBytes += residentCharge(n)
	c.hirEntries++
	c.moveQTail(n)
	c.stackPruning()
}

func (c *Cache) shouldWarmupAsLIR(charge int64) bool {
	if c.capacityBytes <= 0 || c.lirMaxBytes <= 0 {
		return false
	}
	if c.lirEntries == 0 && c.bytesUsed+charge <= c.capacityBytes {
		return true
	}
	return c.lirBytes < c.lirMaxBytes && c.lirBytes+charge <= c.lirMaxBytes && c.bytesUsed+charge <= c.capacityBytes
}

func (c *Cache) ensureSpaceFor(additionalCharge int64, exclude *node) bool {
	for c.bytesUsed+additionalCharge > c.capacityBytes {
		if c.evictOneHIR(exclude) {
			c.stackPruning()
			continue
		}
		if c.demoteBottomLIR(exclude) {
			c.stackPruning()
			continue
		}
		return false
	}
	return true
}

func (c *Cache) evictOneHIR(exclude *node) bool {
	victim := c.hirQ.head
	if victim == nil {
		return false
	}
	if victim == exclude {
		if victim.qNext == nil {
			return false
		}
		c.moveQTail(victim)
		victim = c.hirQ.head
		if victim == nil || victim == exclude {
			return false
		}
	}

	c.removeQ(victim)
	if victim.isResident {
		charge := residentCharge(victim)
		victim.isResident = false
		victim.value = nil
		c.bytesUsed -= victim.size
		c.valueBytes -= victim.size
		if c.bytesUsed < 0 {
			c.bytesUsed = 0
		}
		if c.valueBytes < 0 {
			c.valueBytes = 0
		}
		if victim.inStack {
			c.stackTrackedBytes -= victim.size
			if c.stackTrackedBytes < 0 {
				c.stackTrackedBytes = 0
			}
		}
		c.hirBytes -= charge
		if c.hirBytes < 0 {
			c.hirBytes = 0
		}
		c.residentEntries--
		if c.residentEntries < 0 {
			c.residentEntries = 0
		}
		c.hirEntries--
		if c.hirEntries < 0 {
			c.hirEntries = 0
		}
		c.evictions++
	}
	if !victim.inStack {
		c.bytesUsed -= victim.keySize
		c.keyBytes -= victim.keySize
		if c.bytesUsed < 0 {
			c.bytesUsed = 0
		}
		if c.keyBytes < 0 {
			c.keyBytes = 0
		}
		delete(c.blockMap, victim.key)
	}
	return true
}

func (c *Cache) promoteHIRToLIR(n *node) {
	if n == nil || n.status == statusLIR {
		return
	}
	n.status = statusLIR
	if n.isResident {
		charge := residentCharge(n)
		c.hirBytes -= charge
		if c.hirBytes < 0 {
			c.hirBytes = 0
		}
		c.lirBytes += charge
		c.hirEntries--
		if c.hirEntries < 0 {
			c.hirEntries = 0
		}
		c.lirEntries++
	}
	c.promotions++
}

func (c *Cache) demoteBottomLIR(exclude *node) bool {
	victim := c.stack.tail
	if victim == nil || victim == exclude || victim.status != statusLIR {
		return false
	}
	victim.status = statusHIR
	if victim.isResident {
		charge := residentCharge(victim)
		c.lirBytes -= charge
		if c.lirBytes < 0 {
			c.lirBytes = 0
		}
		c.hirBytes += charge
		c.lirEntries--
		if c.lirEntries < 0 {
			c.lirEntries = 0
		}
		c.hirEntries++
		c.moveQTail(victim)
	}
	c.demotions++
	return true
}

func (c *Cache) stackPruning() {
	for c.stack.tail != nil && c.stack.tail.status == statusHIR {
		victim := c.stack.tail
		c.removeStack(victim)
		if !victim.isResident && !victim.inQ {
			c.removeMetadataCharge(victim)
			delete(c.blockMap, victim.key)
		}
		c.prunes++
	}
	c.enforceStackLimit()
}

func (c *Cache) enforceStackLimit() {
	if c.maxStackBytes <= 0 {
		return
	}
	for c.stackTrackedBytes > c.maxStackBytes && c.stack.tail != nil && c.stack.tail.status == statusHIR {
		victim := c.stack.tail
		c.removeStack(victim)
		if !victim.isResident && !victim.inQ {
			c.removeMetadataCharge(victim)
			delete(c.blockMap, victim.key)
		}
		c.prunes++
	}
}

func (c *Cache) moveStackTop(n *node) {
	if n == nil {
		return
	}
	if n.inStack {
		c.removeStack(n)
	}
	c.pushStackFront(n)
}

func (c *Cache) pushStackFront(n *node) {
	n.sPrev = nil
	n.sNext = c.stack.head
	if c.stack.head != nil {
		c.stack.head.sPrev = n
	} else {
		c.stack.tail = n
	}
	c.stack.head = n
	n.inStack = true
	c.stack.len++
	c.stackTrackedBytes += nodeCharge(n)
}

func (c *Cache) removeStack(n *node) {
	if n == nil || !n.inStack {
		return
	}
	if n.sPrev != nil {
		n.sPrev.sNext = n.sNext
	} else {
		c.stack.head = n.sNext
	}
	if n.sNext != nil {
		n.sNext.sPrev = n.sPrev
	} else {
		c.stack.tail = n.sPrev
	}
	n.sPrev = nil
	n.sNext = nil
	n.inStack = false
	if c.stack.len > 0 {
		c.stack.len--
	}
	c.stackTrackedBytes -= nodeCharge(n)
	if c.stackTrackedBytes < 0 {
		c.stackTrackedBytes = 0
	}
}

func (c *Cache) moveQTail(n *node) {
	if n == nil {
		return
	}
	if n.inQ {
		c.removeQ(n)
	}
	c.pushQTail(n)
}

func (c *Cache) pushQTail(n *node) {
	n.qNext = nil
	n.qPrev = c.hirQ.tail
	if c.hirQ.tail != nil {
		c.hirQ.tail.qNext = n
	} else {
		c.hirQ.head = n
	}
	c.hirQ.tail = n
	n.inQ = true
	c.hirQ.len++
}

func (c *Cache) removeQ(n *node) {
	if n == nil || !n.inQ {
		return
	}
	if n.qPrev != nil {
		n.qPrev.qNext = n.qNext
	} else {
		c.hirQ.head = n.qNext
	}
	if n.qNext != nil {
		n.qNext.qPrev = n.qPrev
	} else {
		c.hirQ.tail = n.qPrev
	}
	n.qPrev = nil
	n.qNext = nil
	n.inQ = false
	if c.hirQ.len > 0 {
		c.hirQ.len--
	}
}

func keySize(key string) int64 {
	trimmed := strings.TrimPrefix(strings.TrimSpace(key), "0x")
	if trimmed == "" {
		return 0
	}
	if len(trimmed)%2 == 0 && isHex(trimmed) {
		return int64(len(trimmed) / 2)
	}
	return int64(len(key))
}

func isHex(value string) bool {
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func residentCharge(n *node) int64 {
	if n == nil {
		return 0
	}
	return n.keySize + n.size
}

func nodeCharge(n *node) int64 {
	if n == nil {
		return 0
	}
	if n.isResident {
		return residentCharge(n)
	}
	return n.keySize
}

func (c *Cache) removeMetadataCharge(n *node) {
	if n == nil {
		return
	}
	c.bytesUsed -= n.keySize
	c.keyBytes -= n.keySize
	if c.bytesUsed < 0 {
		c.bytesUsed = 0
	}
	if c.keyBytes < 0 {
		c.keyBytes = 0
	}
}
