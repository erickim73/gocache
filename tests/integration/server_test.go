package integration_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/server"
	"github.com/erickim73/gocache/tests/harness"
)

// confirms the full wiring from harness -> server.New() -> real TCP connection is working
func TestSmokePing(t *testing.T) {
	h := harness.New(t)

	leader := h.StartLeader()

	client := h.Client(leader)

	got := client.Ping()

	if got != "PONG" {
		t.Errorf("expected PONG, got %q", got)
	}
}				

// verifies that Stop() releases the TCP port back to the os
func TestCleanShutdown(t *testing.T) {
	h := harness.New(t)

	leader := h.StartLeader()
	port := leader.Port()

	// verify server is actually up before we try to stop it
	client := h.Client(leader)
	if got := client.Ping(); got != "PONG" {
		t.Fatalf("pre-condition failed: expected PONG before shutdown, got %q", got)
	}

	leader.Stop()

	// try to bind the same port
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	var bindErr error
	for i := 0; i < 20; i++ {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			// Successfully bound — port is free. Release it and pass.
			ln.Close()
			return
		}
		bindErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("port %d still in use 200ms after Stop(): %v", port, bindErr)
}

// verifies that a second server can start on the same port immediately after the first one starts
func TestRestart(t *testing.T) {
	h := harness.New(t)

	// first server
	first := h.StartLeader()
	port := first.Port()

	client1 := h.Client(first)
	if got := client1.Ping(); got != "PONG" {
		t.Fatalf("first server: expected PONG, got %q", got)
	}

	// stop the first server. After this returns the port is free.
	first.Stop()

	// second server on the same port
	serverCfg := config.DefaultConfig()
	serverCfg.Port = port
	serverCfg.Role = "leader"
	serverCfg.MaxCacheSize = 64 * 1024 * 1024
	serverCfg.AOFFileName = t.TempDir() + "/restart.aof"
	serverCfg.SnapshotFileName = t.TempDir() + "/restart.snap"

	srv := server.New(serverCfg)

	// Start() blocks, so run it in a goroutine.
	doneCh := make(chan struct{}, 1)
	go func() {
		srv.Start()
		doneCh <- struct{}{}
	}()

	// register cleanup so the test never leaks a server even on failure.
	t.Cleanup(func() {
		srv.Stop()
		<-doneCh
	})

	// wait for the second server to be ready.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if err := waitForPort(t, addr, 3*time.Second); err != nil {
		t.Fatalf("second server never became ready on port %d: %v", port, err)
	}

	// confirm the second server responds correctly.
	client2 := harness.NewClient(t, addr)
	if got := client2.Ping(); got != "PONG" {
		t.Errorf("second server: expected PONG after restart, got %q", got)
	}
}

// waitForPort dials addr in a loop until the connection succeeds or timeout expires.
func waitForPort(t *testing.T, addr string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	delay := time.Millisecond
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(delay)
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
	return fmt.Errorf("server at %s not ready after %s", addr, timeout)
}