package cache

import "time"

// returns all keys whose hash values fall within [start, end)
func (c *Cache) GetKeysInHashRange(start uint32, end uint32, hashFunc func(string) uint32) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	matching := []string{}

	for key := range c.data {
		keyHash := hashFunc(key)

		// check if key's hash falls within our target range
		if hashInRange(keyHash, start, end) {
			matching = append(matching, key)
		}
	}

	return matching
}

// checks if a hash falls within a range
func hashInRange(hash uint32, start uint32, end uint32) bool {
	// normal range - no wrap around
	if start < end {
		return hash >= start && hash < end
	}

	// wrap around range
	return hash >= start || hash < end
}

// returns keys and their values in a hash range
func (c *Cache) GetKeysWithValues(start uint32, end uint32, hashFunc func(string) uint32) map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]string)

	for key, item := range c.data {
		// skip expired items
		if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
			continue
		}

		// check if key is in our target hash range
		keyHash := hashFunc(key)
		if hashInRange(keyHash, start, end) {
			result[key] = item.value
		}
	}

	return result
}

// returns the number of keys in a hash range
func (c *Cache) CountKeysInRange(start uint32, end uint32, hashFunc func(string) uint32) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	count := 0

	for key := range c.data {
		keyHash := hashFunc(key)
		if hashInRange(keyHash, start, end) {
			count++
		}
	}

	return count
}
