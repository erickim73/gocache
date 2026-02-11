package cache

import (
	"sync"
	"time"
	"github.com/erickim73/gocache/internal/lru"
	"github.com/erickim73/gocache/internal/metrics"
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
	metrics *metrics.Collector // metrics collector for recording cache operations
	currentMemoryBytes int64 // track current memory usage for metrics
}

type SnapshotEntry struct {
	Value string
	ExpiresAt time.Time
}

func NewCache(maxSize int, metricsCollector *metrics.Collector) (*Cache, error) {	
	return &Cache{
		data: make(map[string]*CacheItem),
		lru: &lru.LRU{},
		maxSize: maxSize,
		metrics: metricsCollector,
		currentMemoryBytes: 0,
	}, nil
}

func (c *Cache) Set(key, value string, ttl time.Duration) error {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()

	// check if key exists
	existingItem, exists := c.data[key]

	// if updating existing key, subtract old memory first
	if exists {
		oldSize := c.calculateItemSize(key, existingItem.value)
		c.currentMemoryBytes -= oldSize
	}

	// if cache is full and key doesn't exist
	if !exists && len(c.data) >= c.maxSize {
		// before evicting, subtract evicted item's memory
		evictedKey := c.lru.RemoveLRU()
		evictedItem, found := c.data[evictedKey]
		if found {
			evictedSize := c.calculateItemSize(evictedKey, evictedItem.value)
			c.currentMemoryBytes -= evictedSize
			c.metrics.RecordEviction() // record that an eviction occured
		}
		delete(c.data, evictedKey)
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

	// calculate and add new memory
	newSize := c.calculateItemSize(key, value)
	c.currentMemoryBytes += newSize

	// record metrics after successful operation
	c.metrics.RecordOperation("set")
	c.metrics.UpdateItemsCount(len(c.data))
	c.metrics.UpdateMemoryUsage(c.currentMemoryBytes)

	return nil
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock() 		// multiple readers can enter
	defer c.mu.RUnlock()

	c.metrics.RecordOperation("get")
	
	node, exists := c.data[key]
	
	// key doesn't exist in cache
	if !exists {
		c.metrics.RecordCacheMiss()
		return "", false
	}

	// key exists but has expired
	if !node.expiresAt.IsZero() && time.Now().After(node.expiresAt) {
		c.metrics.RecordCacheMiss()
		c.metrics.RecordExpiration()
		return "", false
	}
	
	// key exists and is valid
	c.metrics.RecordCacheHit()
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