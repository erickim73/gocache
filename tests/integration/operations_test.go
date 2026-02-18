package integration_test

import (
	"testing"
	"time"

	"github.com/erickim73/gocache/tests/harness"
)

// verifies the basic write-read-delete cycle works correctly
func TestSetGetDelRoundTrip(t *testing.T) {
	h := harness.New(t)
	leader := h.StartLeader()
	client := h.Client(leader)

	// write a key
	if got := client.Set("myKey", "myValue"); got != "OK" {
		t.Fatalf("SET failed: expected OK, got %q", got)
	}

	// read it back
	if got := client.Get("myKey"); got != "myValue" {
		t.Errorf("GET after SET: expected myValue, got %q", got)
	}

	// delete key
	if got := client.Del("myKey"); got != "OK" {
		t.Fatalf("DEL failed: expected OK, got %q", got)
	}

	// verify it's gone
	if got := client.Get("myKey"); got != "" {
		t.Errorf("GET after DEL: expected empty string, got %q", got)
	}
}

// verifies that GET on a key that was never SET returns an empty string rather than an error or crashing
func TestGetMissingKey(t *testing.T) {
	h := harness.New(t)
	leader := h.StartLeader()
	client := h.Client(leader)

	// GET a key that was never SET
	got := client.Get("nonexistent-key")

	// should return empty string
	if got != "" {
		t.Errorf("GET on missing key: expected empty string, got %q", got)
	}

	// verify server is still functional after the missing key lookup
	if pong := client.Ping(); pong != "PONG" {
		t.Errorf("after missing key lookup: PING returned %q, want PONG", pong)
	}
}

// verifies that keys with a TTL are automatically removed from the cache after the TTL expires
func TestTTLExpiration(t *testing.T) {
	h := harness.New(t)
	leader := h.StartLeader()
	client := h.Client(leader)

	// set a key with 1 second ttl
	if got := client.SetEx("ttl-key", "ttl-value", 1); got != "OK" {
		t.Fatalf("SET with TTL failed: expected OK, got %q", got)
	}

	// immediately verify the key exists
	if got := client.Get("ttl-key"); got != "ttl-value" {
		t.Errorf("GET immediately after SET: expected ttl-value, got %q", got)
	}

	// wait for key to expire
	time.Sleep(1500 * time.Millisecond)
	
	// verify key has been removed
	if got := client.Get("ttl-key"); got != "" {
		t.Errorf("GET after TTL expired: expected empty string (key gone), got %q", got)
	}

	// verify server is still functional 
	if pong := client.Ping(); pong != "PONG" {
		t.Errorf("after TTL expiration: PING returned %q, want PONG", pong)
	}
}

// verifies that SET/GET/DEL on one key doesn't affect other keys in the cache
func TestMultipleKeysIndependent(t *testing.T) {
	h := harness.New(t)
	leader := h.StartLeader()
	client := h.Client(leader)

	// write three keys
	client.Set("key1", "value1")
	client.Set("key2", "value2")
	client.Set("key3", "value3")

	// delete the middle key
	client.Del("key2")

	// verify key1 and key3 are still there
	if got := client.Get("key1"); got != "value1" {
		t.Errorf("key1 after deleting key2: expected value1, got %q", got)
	}
	if got := client.Get("key3"); got != "value3" {
		t.Errorf("key3 after deleting key2: expected value3, got %q", got)
	}

	// verify key2 is gone
	if got := client.Get("key2"); got != "" {
		t.Errorf("key2 after delete: expected empty string, got %q", got)
	}
}

// verifies that SET on an existing key updates the value rather than creating a duplicate entry or returning an error
func TestSetOverwrite(t *testing.T) {
	h := harness.New(t)
	leader := h.StartLeader()
	client := h.Client(leader)

	// write a key
	client.Set("overwrite-key", "original")

	// verify original value is there
	if got := client.Get("overwrite-key"); got != "original" {
		t.Errorf("first GET: expected original, got %q", got)
	}

	// overwrite with a new value
	if got := client.Set("overwrite-key", "updated"); got != "OK" {
		t.Fatalf("SET overwrite failed: expected OK, got %q", got)
	}

	// verify new value replaced the old one
	if got := client.Get("overwrite-key"); got != "updated" {
		t.Errorf("GET after overwrite: expected updated, got %q", got)
	}
}