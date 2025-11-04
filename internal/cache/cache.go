package cache

import (
	"sync" 
	"time"
	"errors"
)

type node struct {
	key string
	value string
	expiresAt time.Time
	prev *node
	next *node
}

type Cache struct {
	data map[string]*node
	maxSize int
	head *node
	tail *node
	mu sync.RWMutex   // read write lock
}

func New(maxSize int) *Cache {
	return &Cache{
		data: make(map[string]*node),
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

	c.data[key] = &node{
		key: key,
		value: value,
		expiresAt: expiresAt,
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
	
	c.moveToFront(node)
	return node.value, true
}

func (c *Cache) Delete(key string) {
	c.mu.Lock() 		// exclusive access for writes
	defer c.mu.Unlock()

	delete(c.data, key)	
}

// removes node from the doubly linked list
func (c *Cache) removeNode(node *node) {
	// Only node in the list
	if c.head == node && c.tail == node { 
		c.head = nil
		c.tail = nil
		return
	} 
	
	// Node is at head
	if c.head == node { 
		c.head = node.next
		node.next.prev = nil
		return
	} 
		
	// Node is at tail
	if c.tail == node { 
		c.tail = node.prev
		node.prev.next = nil
		return
	} 
	
	// Node is in middle
	node.prev.next = node.next
	node.next.prev = node.prev
}

// adds a node to the front of the list
func (c *Cache) addToFront(node *node) {
	// Empty list
	if c.head == nil && c.tail == nil { 
		c.head = node
		c.tail = node
		node.next = nil
		node.prev = nil
		return
	} 

	// Non-empty list
	node.prev = nil
	node.next = c.head
	c.head.prev = node
	c.head = node
}


func (c *Cache) moveToFront(node *node) {
	// Node is at head
	if c.head == node { 
		return
	} 

	c.removeNode(node)
	c.addToFront(node)
}