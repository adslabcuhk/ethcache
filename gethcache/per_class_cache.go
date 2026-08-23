package gethcache

import "sync"

const lruEntryOverheadBytes int64 = 96

type cacheLimitKind int

const (
	cacheLimitBytes cacheLimitKind = iota
	cacheLimitEntries
)

type perClassCacheConfig struct {
	limitKind       cacheLimitKind
	capacityBytes   int64
	capacityEntries int
}

type cacheRuntimeStats struct {
	LimitKind       cacheLimitKind
	CapacityBytes   int64
	CapacityEntries int
	CurrentBytes    int64
	CurrentEntries  int
	MaxBytes        int64
	MaxEntries      int
}

type cachedItem struct {
	value []byte
	size  int64
}

type perClassCache struct {
	limitKind       cacheLimitKind
	capBytes        int64
	capEntries      int
	usage           int64
	maxUsage        int64
	maxEntries      int
	lru             BasicLRU[string, *cachedItem]
	mu              sync.Mutex
}

func newPerClassCache(cfg perClassCacheConfig) *perClassCache {
	if cfg.limitKind == cacheLimitBytes && cfg.capacityBytes <= 0 {
		cfg.capacityBytes = 1
	}
	if cfg.limitKind == cacheLimitEntries && cfg.capacityEntries <= 0 {
		cfg.capacityEntries = 1
	}

	lruCapacity := cfg.capacityEntries
	if cfg.limitKind == cacheLimitBytes {
		lruCapacity = int(cfg.capacityBytes)
		if lruCapacity <= 0 {
			lruCapacity = 1
		}
	}

	return &perClassCache{
		limitKind:       cfg.limitKind,
		capBytes:        cfg.capacityBytes,
		capEntries:      cfg.capacityEntries,
		lru:             NewBasicLRU[string, *cachedItem](lruCapacity),
	}
}

func (c *perClassCache) put(key string, value []byte) {
	if key == "" {
		return
	}
	size := c.estimateEntryCharge(key, value)
	if c.limitKind == cacheLimitBytes && size > c.capBytes {
		c.remove(key)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.lru.Peek(key); ok {
		c.usage -= existing.size
		existing.value = cloneBytes(value)
		existing.size = size
		c.usage += size
		c.lru.Add(key, existing)
		c.updateWatermarksLocked()
		return
	}

	if c.limitKind == cacheLimitBytes {
		for c.usage+size > c.capBytes {
			c.removeOldestLocked()
		}
	} else {
		for c.lru.Len() >= c.capEntries {
			c.removeOldestLocked()
		}
	}

	entry := &cachedItem{value: cloneBytes(value), size: size}
	c.lru.Add(key, entry)
	c.usage += size
	c.updateWatermarksLocked()
}

func (c *perClassCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.lru.Get(key)
	if !ok {
		return nil, false
	}
	return cloneBytes(entry.value), true
}

func (c *perClassCache) remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.lru.Peek(key)
	if !ok {
		return
	}
	if c.lru.Remove(key) {
		c.usage -= entry.size
		if c.usage < 0 {
			c.usage = 0
		}
	}
}

func (c *perClassCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

func (c *perClassCache) currentUsage() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

func (c *perClassCache) capacityBytes() int64 {
	if c.limitKind != cacheLimitBytes {
		return 0
	}
	return c.capBytes
}

func (c *perClassCache) capacityEntries() int {
	if c.limitKind != cacheLimitEntries {
		return 0
	}
	return c.capEntries
}

func (c *perClassCache) stats() cacheRuntimeStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	return cacheRuntimeStats{
		LimitKind:       c.limitKind,
		CapacityBytes:   c.capBytes,
		CapacityEntries: c.capEntries,
		CurrentBytes:    c.usage,
		CurrentEntries:  c.lru.Len(),
		MaxBytes:        c.maxUsage,
		MaxEntries:      c.maxEntries,
	}
}

func (c *perClassCache) removeOldestLocked() {
	_, entry, ok := c.lru.RemoveOldest()
	if !ok {
		return
	}
	c.usage -= entry.size
	if c.usage < 0 {
		c.usage = 0
	}
}

func (c *perClassCache) updateWatermarksLocked() {
	if c.usage > c.maxUsage {
		c.maxUsage = c.usage
	}
	if entries := c.lru.Len(); entries > c.maxEntries {
		c.maxEntries = entries
	}
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func estimateEntrySizeBytes(key string, value []byte) int64 {
	return lruEntryOverheadBytes + int64(len(key)) + int64(len(value))
}

func estimateEntrySizeKVBytes(key string, value []byte) int64 {
	return int64(len(key)) + int64(len(value))
}

func (c *perClassCache) estimateEntryCharge(key string, value []byte) int64 {
	if c.limitKind == cacheLimitEntries {
		return estimateEntrySizeKVBytes(key, value)
	}
	return estimateEntrySizeBytes(key, value)
}
