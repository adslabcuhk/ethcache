package ethcache

import (
	"sort"
	"sync/atomic"
	"unsafe"
)

const (
	// set as 0 for fair comparison against Geth's cache which doesn't track entry overhead separately
	estimatedEntryOverheadBytes int64 = 0
)

// Cache implements an LFU+LRU cache using a multi-level frequency queue (MLFQ).
// The cache consists of a primary index for direct key lookups and a set of
// doubly linked lists that enforce frequency-based protection and recency order
// within each frequency band.
type Cache[K comparable, V any] struct {
	byteCapacity int64
	bytesUsed    int64
	size         int
	level1TTL    uint64
	index        map[K]*cacheEntry[K, V]
	bands        []frequencyBand[K, V]
	thresholds   []int
	globalTick   atomic.Uint64

	promotionsTotal           uint64
	crossLevelPromotionsTotal uint64
	sameLevelRefreshTotal     uint64
	evictionsTotal            uint64
	promotionsByFromTo        [][]uint64
	evictionsByLevel          []uint64
	evictedBytesByLevel       []int64
}

type LevelStats struct {
	LevelID int
	MinFreq int
	MaxFreq int
	Entries int
	Bytes   int64
}

type StatsSnapshot struct {
	Thresholds                []int
	PromotionsTotal           uint64
	CrossLevelPromotionsTotal uint64
	SameLevelRefreshTotal     uint64
	EvictionsTotal            uint64
	PromotionsByFromTo        [][]uint64
	EvictionsByLevel          []uint64
	EvictedBytesByLevel       []int64
	Levels                    []LevelStats
}

type cacheEntry[K comparable, V any] struct {
	key              K
	value            V
	sizeBytes        int64
	lastAccessedTick uint64
	block            *controlBlock[K, V]
}

type controlBlock[K comparable, V any] struct {
	freq  int
	items []K
	entry *cacheEntry[K, V]
	prev  *controlBlock[K, V]
	next  *controlBlock[K, V]
	level *level[K, V]
}

type level[K comparable, V any] struct {
	id   int
	head *controlBlock[K, V]
	tail *controlBlock[K, V]
}

type frequencyBand[K comparable, V any] struct {
	min   int
	max   int
	level *level[K, V]
}

// NewCache builds a cache with the provided capacity and level thresholds.
// When thresholds are empty, defaults of [1,2,4,8] are used. Thresholds define
// the minimum frequency needed to place an entry into the corresponding level.
func NewCache[K comparable, V any](byteCapacity int64, thresholds []int) *Cache[K, V] {
	if byteCapacity < 0 {
		byteCapacity = 0
	}

	normalized := normalizeThresholds(thresholds)
	levels := make([]*level[K, V], len(normalized))
	bands := make([]frequencyBand[K, V], len(normalized))

	for idx := range levels {
		levels[idx] = &level[K, V]{id: idx}
	}

	for idx, min := range normalized {
		band := frequencyBand[K, V]{min: min, level: levels[idx]}
		if idx < len(normalized)-1 {
			next := normalized[idx+1]
			if next <= min {
				next = min + 1
			}
			band.max = next - 1
		}
		bands[idx] = band
	}

	initialCap := 0
	if byteCapacity > 0 {
		initialCap = int(byteCapacity / 256)
		if initialCap < 1 {
			initialCap = 1
		}
		if initialCap > 1024 {
			initialCap = 1024
		}
	}

	return &Cache[K, V]{
		byteCapacity:        byteCapacity,
		index:               make(map[K]*cacheEntry[K, V], initialCap),
		bands:               bands,
		thresholds:          append([]int(nil), normalized...),
		promotionsByFromTo:  makeMatrix(len(normalized)),
		evictionsByLevel:    make([]uint64, len(normalized)),
		evictedBytesByLevel: make([]int64, len(normalized)),
	}
}

func (c *Cache[K, V]) SetLevel1TTL(level1TTL uint64) {
	c.level1TTL = level1TTL
}

func makeMatrix(size int) [][]uint64 {
	if size <= 0 {
		return nil
	}
	matrix := make([][]uint64, size)
	for i := range matrix {
		matrix[i] = make([]uint64, size)
	}
	return matrix
}

// Get retrieves a value and updates its recency/frequency metadata.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	tick := c.globalTick.Add(1)
	c.demoteExpiredLevelOneTail(tick)

	entry, ok := c.index[key]
	if !ok {
		var zero V
		return zero, false
	}

	entry.lastAccessedTick = tick
	c.promote(entry)
	return entry.value, true
}

func (c *Cache[K, V]) demoteExpiredLevelOneTail(currentTick uint64) {
	if c.level1TTL == 0 || len(c.bands) < 2 {
		return
	}

	level0 := c.bands[0].level
	level1 := c.bands[1].level
	if level0 == nil || level1 == nil {
		return
	}

	level0MaxFreq := c.level0MaxFreq()
	for {
		tail := level1.tail
		if tail == nil || tail.entry == nil {
			return
		}

		lastTick := tail.entry.lastAccessedTick
		if currentTick <= lastTick || currentTick-lastTick <= c.level1TTL {
			return
		}

		level1.detach(tail)
		tail.freq = level0MaxFreq
		level0.attachFront(tail)
	}
}

func (c *Cache[K, V]) level0MaxFreq() int {
	if len(c.bands) == 0 {
		return 1
	}

	if c.bands[0].max > 0 {
		return c.bands[0].max
	}
	if c.bands[0].min > 0 {
		return c.bands[0].min
	}
	return 1
}

// Put inserts or updates the key-value pair in the cache. Inserts may trigger
// evictions when the cache is full.
func (c *Cache[K, V]) Put(key K, value V) {
	if c.byteCapacity == 0 {
		return
	}

	newEntryBytes := estimateEntryBytes(key, value)
	if newEntryBytes > c.byteCapacity {
		if existing, ok := c.index[key]; ok {
			c.deleteEntry(existing)
		}
		return
	}

	if entry, ok := c.index[key]; ok {
		delta := newEntryBytes - entry.sizeBytes
		excluded := map[K]struct{}{key: {}}
		for delta > 0 && c.bytesUsed+delta > c.byteCapacity {
			if !c.evictOneExcept(excluded) {
				break
			}
		}
		if delta > 0 && c.bytesUsed+delta > c.byteCapacity {
			return
		}
		entry.value = value
		entry.sizeBytes = newEntryBytes
		c.bytesUsed += delta
		c.promote(entry)
		return
	}

	for c.bytesUsed+newEntryBytes > c.byteCapacity {
		if !c.evictOneExcept(nil) {
			break
		}
	}

	if c.bytesUsed+newEntryBytes > c.byteCapacity {
		return
	}

	block := &controlBlock[K, V]{
		freq:  1,
		items: []K{key},
	}
	entry := &cacheEntry[K, V]{
		key:       key,
		value:     value,
		sizeBytes: newEntryBytes,
		block:     block,
	}
	block.entry = entry

	c.index[key] = entry
	c.size++
	c.bytesUsed += newEntryBytes

	lvl := c.levelForFreq(block.freq)
	lvl.attachFront(block)
}

// Delete removes a key from the cache. Returns true when the key existed.
func (c *Cache[K, V]) Delete(key K) bool {
	entry, ok := c.index[key]
	if !ok {
		return false
	}

	c.deleteEntry(entry)
	return true
}

// Len returns the number of entries stored.
func (c *Cache[K, V]) Len() int {
	return c.size
}

// Capacity returns the configured maximum cache size in bytes.
func (c *Cache[K, V]) Capacity() int64 {
	return c.byteCapacity
}

// ByteCapacity returns the configured maximum cache size in bytes.
func (c *Cache[K, V]) ByteCapacity() int64 {
	return c.byteCapacity
}

// BytesUsed returns the current estimated byte usage.
func (c *Cache[K, V]) BytesUsed() int64 {
	return c.bytesUsed
}

// Clear removes every entry from the cache.
func (c *Cache[K, V]) Clear() {
	for key, entry := range c.index {
		c.removeEntry(entry)
		delete(c.index, key)
	}
	c.size = 0
	c.bytesUsed = 0
}

func (c *Cache[K, V]) deleteEntry(entry *cacheEntry[K, V]) {
	if entry == nil {
		return
	}

	c.removeEntry(entry)
	delete(c.index, entry.key)
	c.size--
	if c.size < 0 {
		c.size = 0
	}
	c.bytesUsed -= entry.sizeBytes
	if c.bytesUsed < 0 {
		c.bytesUsed = 0
	}
}

func (c *Cache[K, V]) promote(entry *cacheEntry[K, V]) {
	block := entry.block
	if block == nil {
		return
	}

	block.freq++
	c.promotionsTotal++
	target := c.levelForFreq(block.freq)
	current := block.level
	if current == nil {
		target.attachFront(block)
		if target != nil {
			c.sameLevelRefreshTotal++
			c.recordPromotion(target.id, target.id)
		}
		return
	}

	if current == target {
		current.moveToFront(block)
		c.sameLevelRefreshTotal++
		c.recordPromotion(current.id, current.id)
		return
	}

	c.crossLevelPromotionsTotal++
	c.recordPromotion(current.id, target.id)
	current.detach(block)
	target.attachFront(block)
}

func (c *Cache[K, V]) recordPromotion(from, to int) {
	if from < 0 || to < 0 || from >= len(c.promotionsByFromTo) || to >= len(c.promotionsByFromTo[from]) {
		return
	}
	c.promotionsByFromTo[from][to]++
}

func (c *Cache[K, V]) removeEntry(entry *cacheEntry[K, V]) {
	block := entry.block
	if block != nil {
		if block.level != nil {
			block.level.detach(block)
		}
		block.items = nil
		block.entry = nil
	}
	entry.block = nil
}

func (c *Cache[K, V]) evictOneExcept(excluded map[K]struct{}) bool {
	for _, band := range c.bands {
		lvl := band.level
		victim := lvl.tail
		for victim != nil {
			if !blockHasExcludedKey(victim, excluded) {
				break
			}
			victim = victim.prev
		}
		if victim == nil {
			continue
		}

		lvl.detach(victim)
		if lvl.id >= 0 && lvl.id < len(c.evictionsByLevel) {
			c.evictionsByLevel[lvl.id]++
		}
		c.evictionsTotal++
		var evictedBytes int64
		for _, key := range victim.items {
			if entry, ok := c.index[key]; ok {
				evictedBytes += entry.sizeBytes
				c.bytesUsed -= entry.sizeBytes
				if c.bytesUsed < 0 {
					c.bytesUsed = 0
				}
				entry.block = nil
				delete(c.index, key)
				c.size--
			}
		}
		if lvl.id >= 0 && lvl.id < len(c.evictedBytesByLevel) {
			c.evictedBytesByLevel[lvl.id] += evictedBytes
		}

		victim.items = nil
		victim.entry = nil
		return true
	}

	return false
}

func blockHasExcludedKey[K comparable, V any](block *controlBlock[K, V], excluded map[K]struct{}) bool {
	if block == nil || len(excluded) == 0 {
		return false
	}
	for _, key := range block.items {
		if _, ok := excluded[key]; ok {
			return true
		}
	}
	return false
}

func estimateEntryBytes[K comparable, V any](key K, value V) int64 {
	return estimatedEntryOverheadBytes + estimateKeyBytes(key) + estimateValueBytes(value)
}

func estimateKeyBytes[K comparable](key K) int64 {
	switch k := any(key).(type) {
	case string:
		return int64(len(k))
	case []byte:
		return int64(len(k))
	default:
		return int64(unsafe.Sizeof(key))
	}
}

func estimateValueBytes[V any](value V) int64 {
	switch v := any(value).(type) {
	case []byte:
		return int64(len(v))
	case string:
		return int64(len(v))
	default:
		return int64(unsafe.Sizeof(value))
	}
}

func (c *Cache[K, V]) levelForFreq(freq int) *level[K, V] {
	if freq < 1 {
		freq = 1
	}

	last := c.bands[len(c.bands)-1]
	for _, band := range c.bands {
		if freq < band.min {
			continue
		}
		if band.max == 0 || freq <= band.max {
			return band.level
		}
	}

	return last.level
}

func normalizeThresholds(raw []int) []int {
	if len(raw) == 0 {
		return []int{1, 2, 4, 8}
	}

	cleaned := make([]int, 0, len(raw))
	for _, v := range raw {
		if v < 1 {
			v = 1
		}
		cleaned = append(cleaned, v)
	}
	sort.Ints(cleaned)

	result := make([]int, 0, len(cleaned))
	for _, v := range cleaned {
		n := len(result)
		if n == 0 || v != result[n-1] {
			result = append(result, v)
		}
	}

	if len(result) == 0 || result[0] != 1 {
		result = append([]int{1}, result...)
	}

	return result
}

func (c *Cache[K, V]) SnapshotStats() StatsSnapshot {
	levelStats := make([]LevelStats, 0, len(c.bands))
	for _, band := range c.bands {
		entries := 0
		bytes := int64(0)
		for node := band.level.head; node != nil; node = node.next {
			entries += len(node.items)
			if node.entry != nil {
				bytes += node.entry.sizeBytes
			}
		}
		levelStats = append(levelStats, LevelStats{
			LevelID: band.level.id,
			MinFreq: band.min,
			MaxFreq: band.max,
			Entries: entries,
			Bytes:   bytes,
		})
	}

	promotions := make([][]uint64, len(c.promotionsByFromTo))
	for i := range c.promotionsByFromTo {
		promotions[i] = append([]uint64(nil), c.promotionsByFromTo[i]...)
	}

	return StatsSnapshot{
		Thresholds:                append([]int(nil), c.thresholds...),
		PromotionsTotal:           c.promotionsTotal,
		CrossLevelPromotionsTotal: c.crossLevelPromotionsTotal,
		SameLevelRefreshTotal:     c.sameLevelRefreshTotal,
		EvictionsTotal:            c.evictionsTotal,
		PromotionsByFromTo:        promotions,
		EvictionsByLevel:          append([]uint64(nil), c.evictionsByLevel...),
		EvictedBytesByLevel:       append([]int64(nil), c.evictedBytesByLevel...),
		Levels:                    levelStats,
	}
}

func (lvl *level[K, V]) attachFront(block *controlBlock[K, V]) {
	block.level = lvl
	block.prev = nil
	block.next = lvl.head
	if lvl.head != nil {
		lvl.head.prev = block
	}
	lvl.head = block
	if lvl.tail == nil {
		lvl.tail = block
	}
}

func (lvl *level[K, V]) moveToFront(block *controlBlock[K, V]) {
	if block.level != lvl || lvl.head == block {
		return
	}

	lvl.detach(block)
	lvl.attachFront(block)
}

func (lvl *level[K, V]) detach(block *controlBlock[K, V]) {
	if block.level != lvl {
		return
	}

	if block.prev != nil {
		block.prev.next = block.next
	} else {
		lvl.head = block.next
	}

	if block.next != nil {
		block.next.prev = block.prev
	} else {
		lvl.tail = block.prev
	}

	block.prev = nil
	block.next = nil
	block.level = nil
}
