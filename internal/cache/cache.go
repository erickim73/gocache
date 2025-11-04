package cache

import (
	"sync" 
	"time"
	"errors"
)

type item struct {
	value string
	expiresAt time.Time
}

type Cache struct {
	data map[string]*item
	maxSize int
	mu sync.RWMutex   // read write lock
}

func New(maxSize int) *Cache {
	return &Cache{
		data: make(map[string]*item),
		maxSize: maxSize,
	}
}

func (c *Cache) Set(key, value string, ttl time.Duration) error {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()

	_, exists := c.data[key]
	currentSize := len(c.data)

	if !exists && currentSize >= c.maxSize {
		return errors.New("Cache is full")
	}

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	c.data[key] = &item{
		value: value,
		expiresAt: expiresAt,
	}
	return nil	
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock() 		// multiple readers can enter
	defer c.mu.RUnlock()
	
	item, exists := c.data[key]
	
	if !exists {
		return "", false
	}

	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		return "", false
	}
	return item.value, true
}

func (c *Cache) Delete(key string) {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()

	delete(c.data, key)	
}