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

func TestLRUEviction(t *testing.T) {
	cache := New(3)

	cache.Set("A", "1", 0) // [A]
	cache.Set("B", "2", 0) // [B, A]
	cache.Set("C", "3", 0) // [C, B, A]
	cache.Set("D", "4", 0) // [D, C, B]

	if _, exists := cache.Get("A"); exists {
		t.Errorf("This key isn't supposed to exist in the cache")
	}

	if _, exists := cache.Get("B"); !exists {
		t.Errorf("Key 'B' should exist in cache")
	}
	if _, exists := cache.Get("C"); !exists {
		t.Errorf("Key 'C' should exist in cache")
	}
	if _, exists := cache.Get("D"); !exists {
		t.Errorf("Key 'D' should exist in cache")
	}
}