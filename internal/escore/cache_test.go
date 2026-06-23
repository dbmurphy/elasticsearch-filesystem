package escore

import (
	"testing"
	"time"
)

func TestTTLCacheBasic(t *testing.T) {
	c := newTTLCache[int](2, time.Minute)
	c.Put("a", 1)
	c.Put("b", 2)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("get a = %v,%v", v, ok)
	}
	// Inserting c evicts least-recently-used (b, since a was just read).
	c.Put("c", 3)
	if _, ok := c.Get("b"); ok {
		t.Fatalf("b should have been evicted")
	}
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("a should survive")
	}
}

func TestTTLCacheExpiry(t *testing.T) {
	c := newTTLCache[int](8, 10*time.Millisecond)
	now := time.Now()
	c.now = func() time.Time { return now }
	c.Put("k", 5)
	if _, ok := c.Get("k"); !ok {
		t.Fatal("k should be present")
	}
	now = now.Add(20 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("k should have expired")
	}
}
