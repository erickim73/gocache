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

func New(maxSize int) *Cache {
	return &Cache{
		data: make(map[string]*CacheItem),
		maxSize: maxSize,
		lru: &lru.LRU{},
	}
}

func (c *Cache) Set(key, value string, ttl time.Duration) {
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

func (c *Cache) Delete(key string) {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()

	item, exists := c.data[key]

	if !exists {
		return 
	}

	c.lru.RemoveNode(item.node)
	delete(c.data, key)	
}

