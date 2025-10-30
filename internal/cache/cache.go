package cache

type Cache struct {
	data map[string]string
	maxSize int
}

func New(maxSize int) *Cache {
	return &Cache{
		data: make(map[string]string),
		maxSize: maxSize,
	}
}

func (c *Cache) Set(key, value string) {
	c.data[key] = value
}

func (c *Cache) Get(key string) (string, bool) {
	value, exists := c.data[key]
	return value, exists
}

func (c *Cache) Delete(key string) {
	delete(c.data, key)	
}