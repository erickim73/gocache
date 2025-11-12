package cache

import (
	"fmt"
	"sync"
	"time"

	"github.com/erickim73/gocache/internal/lru"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/pkg/protocol"
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
	aof *persistence.AOF
	mu sync.RWMutex   // read write lock
}

func New(maxSize int) (*Cache, error) {	
	aof, err := persistence.NewAOF("aof", persistence.SyncEverySecond)
	if err != nil {
		return nil, err
	}
	
	return &Cache{
		data: make(map[string]*CacheItem),
		lru: &lru.LRU{},
		maxSize: maxSize,
		aof: aof,
	}, nil
}

func (c *Cache) Set(key, value string, ttl time.Duration) error {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()

	// log to aof
	resp := protocol.EncodeArray([]string{"SET", key, value, ttl.String()})
	err := c.aof.Append(resp)
	if err != nil {
		return fmt.Errorf("Error appending to aof: %v", err)
	}

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

	// log to aof
	resp := protocol.EncodeArray([]string{"DEL", key})
	err := c.aof.Append(resp)
	if err != nil {
		return fmt.Errorf("Error appending to resp: %v", err)
	}

	item, exists := c.data[key]

	if !exists {
		return nil
	}

	c.lru.RemoveNode(item.node)
	delete(c.data, key)	

	

	return nil
}

