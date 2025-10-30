package cache

import (
	"testing"
	"time"
	
)

func TestSetAndGet(t *testing.T) {
	cache := New(100)

	cache.Set("eric", "smart", 100 * time.Second)
	result, exists := cache.Get("eric")
	expected := "smart"

	if !exists {
		t.Errorf("This key doesn't exist in the cache")
	}
	if result != expected {
		t.Errorf("Cache[eric] = %s, expected %s", result, expected)
	}
}

func TestInvalidKey(t *testing.T) {
	cache := New(100)

	cache.Set("eric", "smart", 100 * time.Second)
	result, exists := cache.Get("aiden")

	if exists {
		t.Errorf("Key aiden shouldn't exist in the cache but it does")
	}
	if result != "" {
		t.Errorf("Expected empty string for non-existent key, got %q", result)
	}
}