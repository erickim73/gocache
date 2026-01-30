package cache

import (
	"sync"
	"time"
	"github.com/erickim73/gocache/internal/lru"
)


type CacheItem struct {
	value string
	expiresAt time.Time
	node *lru.Node
}

type Cache struct {
	data map[string]*CacheItem
	lru *lru.LRU
	maxSize int
	mu sync.RWMutex   // read write lock
}


type SnapshotEntry struct {
	Value string
	ExpiresAt time.Time
}

func NewCache(maxSize int) (*Cache, error) {	
	return &Cache{
		data: make(map[string]*CacheItem),
		lru: &lru.LRU{},
		maxSize: maxSize,
	}, nil
}

func (c *Cache) Set(key, value string, ttl time.Duration) error {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()

	// check if key exists
	_, exists := c.data[key]

	// if cache is full and key doesn't exist
	if !exists && len(c.data) >= c.maxSize {
		deletedKey := c.lru.RemoveLRU()
		delete(c.data, deletedKey)
	}

	// calculate expiration
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	if exists {
		c.data[key].value = value
		c.data[key].expiresAt = expiresAt
		c.lru.MoveToFront(c.data[key].node)
	} else {
		node := c.lru.Add(key)
		c.data[key] = &CacheItem{
			value: value,
			expiresAt: expiresAt,
			node: node,
		}
	}

	return nil
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock() 		// multiple readers can enter
	defer c.mu.RUnlock()
	
	node, exists := c.data[key]
	
	if !exists {
		return "", false
	}

	if !node.expiresAt.IsZero() && time.Now().After(node.expiresAt) {
		return "", false
	}
	
	c.lru.MoveToFront(node.node)
	return node.value, true
}

func (c *Cache) Delete(key string) error {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()

	item, exists := c.data[key]

	if !exists {
		return nil
	}

	c.lru.RemoveNode(item.node)
	delete(c.data, key)	

	return nil
}

func (c *Cache) Snapshot() map[string]SnapshotEntry {
	snapshot := make(map[string]SnapshotEntry)

	c.mu.Lock()
	for k, v := range c.data {
		snapshot[k] = SnapshotEntry{
			Value: v.value,
			ExpiresAt: v.expiresAt,
		}
	}
	c.mu.Unlock()

	return snapshot
}