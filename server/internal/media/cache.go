package media

import (
	"sync"
	"time"
)

const (
	imgTTL      = 24 * time.Hour
	playlistTTL = time.Hour
	segmentTTL  = time.Hour
)

type cacheEntry struct {
	ctype string
	data  []byte
	exp   time.Time
}

type cacheConfig struct {
	maxEntries    int
	maxBytes      int64
	maxEntryBytes int64
	ttl           time.Duration
}

// byteCache 是带 TTL 与容量上限的内存缓存，超出上限时按任意顺序淘汰旧条目。
type byteCache struct {
	mu    sync.Mutex
	cfg   cacheConfig
	items map[string]cacheEntry
	bytes int64
}

func newByteCache(cfg cacheConfig) *byteCache {
	return &byteCache{cfg: cfg, items: make(map[string]cacheEntry)}
}

func (c *byteCache) get(key string) (string, []byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return "", nil, false
	}
	if time.Now().After(entry.exp) {
		c.bytes -= int64(len(entry.data))
		delete(c.items, key)
		return "", nil, false
	}
	return entry.ctype, entry.data, true
}

func (c *byteCache) put(key, ctype string, data []byte) {
	if c.cfg.maxEntryBytes > 0 && int64(len(data)) > c.cfg.maxEntryBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.items[key]; ok {
		c.bytes -= int64(len(old.data))
		delete(c.items, key)
	}
	for c.overCapacity(int64(len(data))) {
		if !c.evictLocked() {
			break
		}
	}
	c.items[key] = cacheEntry{ctype: ctype, data: data, exp: time.Now().Add(c.cfg.ttl)}
	c.bytes += int64(len(data))
}

func (c *byteCache) overCapacity(incoming int64) bool {
	if c.cfg.maxEntries > 0 && len(c.items) >= c.cfg.maxEntries {
		return true
	}
	return c.cfg.maxBytes > 0 && c.bytes+incoming > c.cfg.maxBytes
}

func (c *byteCache) evictLocked() bool {
	for key, entry := range c.items {
		c.bytes -= int64(len(entry.data))
		delete(c.items, key)
		return true
	}
	return false
}

func (c *byteCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]cacheEntry)
	c.bytes = 0
}