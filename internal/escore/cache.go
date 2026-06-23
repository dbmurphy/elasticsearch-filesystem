package escore

import (
	"container/list"
	"sync"
	"time"
)

// ttlCache is a small bounded LRU with per-entry TTL. It backs the mapping,
// field_caps, index-info, and document caches. Zero TTL disables expiry.
type ttlCache[V any] struct {
	mu       sync.Mutex
	ll       *list.List
	items    map[string]*list.Element
	capacity int
	ttl      time.Duration
	now      func() time.Time
}

type cacheEntry[V any] struct {
	key string
	val V
	exp time.Time
}

func newTTLCache[V any](capacity int, ttl time.Duration) *ttlCache[V] {
	if capacity <= 0 {
		capacity = 1024
	}
	return &ttlCache[V]{
		ll:       list.New(),
		items:    make(map[string]*list.Element),
		capacity: capacity,
		ttl:      ttl,
		now:      time.Now,
	}
}

func (c *ttlCache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	e := el.Value.(*cacheEntry[V])
	if c.ttl > 0 && c.now().After(e.exp) {
		c.removeElement(el)
		var zero V
		return zero, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

func (c *ttlCache[V]) Put(key string, val V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp := time.Time{}
	if c.ttl > 0 {
		exp = c.now().Add(c.ttl)
	}
	if el, ok := c.items[key]; ok {
		e := el.Value.(*cacheEntry[V])
		e.val = val
		e.exp = exp
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&cacheEntry[V]{key: key, val: val, exp: exp})
	c.items[key] = el
	for c.ll.Len() > c.capacity {
		c.removeElement(c.ll.Back())
	}
}

func (c *ttlCache[V]) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.removeElement(el)
	}
}

func (c *ttlCache[V]) removeElement(el *list.Element) {
	if el == nil {
		return
	}
	c.ll.Remove(el)
	delete(c.items, el.Value.(*cacheEntry[V]).key)
}
