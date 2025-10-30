package cache

import "sync"

type Cache struct {
	data map[string]string
	maxSize int
	mu sync.RWMutex   // read write lock
}

func New(maxSize int) *Cache {
	return &Cache{
		data: make(map[string]string),
		maxSize: maxSize,
	}
}

func (c *Cache) Set(key, value string) {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()

	c.data[key] = value
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock() 		// multiple readers can enter
	defer c.mu.RUnlock()
	
	value, exists := c.data[key]
	return value, exists
}

func (c *Cache) Delete(key string) {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()
	
	delete(c.data, key)	
}