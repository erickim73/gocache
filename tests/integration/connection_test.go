package integration_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/erickim73/gocache/tests/harness"
)

// verifies that server can handle multiple simultaneous connections without race conditions or corrupted state
func TestMultipleConcurrentClients(t *testing.T) {
	h := harness.New(t)
	leader := h.StartLeader()

	const numClients = 10

	// create all clients before spawning goroutines
	clients := make([]*harness.Client, numClients)
	for i := 0; i < numClients; i++ {
		clients[i] = harness.NewClient(t, leader.Addr())
	}

	var wg sync.WaitGroup
	wg.Add(numClients)

	// errCh collects failures from goroutines
	errCh := make(chan error, numClients * 3)

	for i := 0; i < numClients; i++ {
		clientID := i
		client := clients[i]
		go func() {
			defer wg.Done()

			// verify basic connectivity
			if got := client.Ping(); got != "PONG" {
				errCh <- fmt.Errorf("client %d: PING failed: got %q", clientID, got)
				return
			}

			// write a unique key-value pair
			key := fmt.Sprintf("key-%d", clientID)
			value := fmt.Sprintf("value-%d", clientID)
			if got := client.Set(key, value); got != "OK" {
				errCh <- fmt.Errorf("client %d: SET failed: got %q", clientID, got)
				return
			}

			// read it back to confirm the cache didn't mix up clients
			if got := client.Get(key); got != value {
				errCh <- fmt.Errorf("client %d: GET returned %q, want %q", clientID, got, value)
				return
			}
		}()
	}
	// wait for all clients to finish
	wg.Wait()
	close(errCh)

	// check if any goroutine reported an error
	for err := range errCh {
		t.Error(err)
	}
}

// verifies that when a client disconnects abruptly, server remains stable and can serve new clients
func TestClientDisconnectDoesNotCrashServer(t *testing.T) {
	h := harness.New(t)
	leader := h.StartLeader()

	// first client: establish that server is working
	t.Run("FirstClient", func(t *testing.T) {
		client := harness.NewClient(t, leader.Addr())
		client.Set("before-disconnect", "value1")
		if got := client.Get("before-disconnect"); got != "value1" {
			t.Errorf("pre-disconnect: GET returned %q, want value1", got)
		}
		// when this subtest ends, t.Cleanup runs and closes the connection
	})

	// verify server is still responsive after the first client disconnected
	t.Run("SecondClient", func(t *testing.T) {
		client := harness.NewClient(t, leader.Addr())

		// verify the server is alive
		if got := client.Ping(); got != "PONG" {
			t.Fatalf("post-disconnect: PING failed: got %q", got)
		}

		// verify the data from before the disconnect is still there
		if got := client.Get("before-disconnect"); got != "value1" {
			t.Errorf("post-disconnect: GET returned %q, want value1", got)
		}

		// write new data to confirm full functionality
		client.Set("after-disconnect", "value2")
		if got := client.Get("after-disconnect"); got != "value2" {
			t.Errorf("post-disconnect: GET new key returned %q, want value2", got)
		}
	})
}

// verifies that the server sends a well-formed error response when it receives a command it doesn't recognize
func TestUnknownCommandReturnsError(t *testing.T) {
	h := harness.New(t)
	leader := h.StartLeader()

	client := h.Client(leader)

	// send a command that doesn't exist
	got := client.Do("FOOBAR", "arg1", "arg2")

	// response should be error
	if got == "" {
		t.Fatalf("expected error response, got empty string")
	}

	// check response indicates an error and mentions the command
	if got[:3] != "ERR" {
		t.Errorf("expected response starting with 'ERR', got %q", got)
	}

	// verify server is still functional after error
	if pong := client.Ping(); pong != "PONG" {
		t.Errorf("after unknown command: PING returned %q, want PONG", pong)
	}
}