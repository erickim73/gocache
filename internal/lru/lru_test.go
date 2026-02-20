package lru

import (
	"testing"
)

func TestLRUEviction(t *testing.T) {
	lru := New(3)

	lru.Add("A")          // [A]
	nodeB := lru.Add("B") // [B, A]
	lru.Add("C")          // [C, B, A]

	if lru.head.key != "C" {
		t.Errorf("The head of the lru should be C, got %s", lru.head.key)
	}
	if lru.tail.key != "A" {
		t.Errorf("The tail of the lru should be A, got %s", lru.tail.key)
	}

	removed := lru.RemoveLRU() // [C, B]

	if removed != "A" {
		t.Errorf("Expected to remove A, got %s", removed)
	}
	if lru.tail.key != "B" {
		t.Errorf("Expected tail of lru to be B, got %s", lru.tail.key)
	}

	lru.MoveToFront(nodeB)
	if lru.head.key != "B" {
		t.Errorf("Expected head to be B, got %s", lru.head.key)
	}
}
