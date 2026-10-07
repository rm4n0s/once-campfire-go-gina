package front

import (
	"container/list"
	"sync"
)

// gzipCache is a size-bounded LRU of compressed bodies keyed by the SHA-256 of
// the uncompressed bytes.
type gzipCache struct {
	mu       sync.Mutex
	limit    int64
	size     int64
	order    *list.List // most recent first
	items    map[[32]byte]*list.Element
	maxEntry int
}

type gzipEntry struct {
	key    [32]byte
	packed []byte
}

func newGzipCache(limit int64) *gzipCache {
	return &gzipCache{limit: limit, order: list.New(), items: map[[32]byte]*list.Element{}, maxEntry: 2 << 20}
}

func (c *gzipCache) get(key [32]byte) ([]byte, bool) {
	if c == nil || c.limit <= 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		return el.Value.(*gzipEntry).packed, true
	}
	return nil, false
}

func (c *gzipCache) put(key [32]byte, packed []byte) {
	if c == nil || c.limit <= 0 || len(packed) > c.maxEntry {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; ok {
		return
	}
	c.items[key] = c.order.PushFront(&gzipEntry{key, packed})
	c.size += int64(len(packed))
	for c.size > c.limit {
		last := c.order.Back()
		entry := c.order.Remove(last).(*gzipEntry)
		delete(c.items, entry.key)
		c.size -= int64(len(entry.packed))
	}
}
