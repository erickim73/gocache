// Package harness provides test infrastructure for GoCache integration and E2E tests.
package harness

import (
	"bufio"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/server"

)

// portTracker prevents two tests running in the same process from claiming the same port
var (
	usedPorts   = make(map[int]bool)
	usedPortsMu sync.Mutex
)

// asks the OS for a free port by binding to :0, records the port so parallel tests in the same process don't claim it twice, then releases the listener so the server can bind to that port.
func getFreePort(t testing.TB) int {
	t.Helper()
	usedPortsMu.Lock()
	defer usedPortsMu.Unlock()

	for i := 0; i < 20; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close() // release it — our server will bind here shortly

		// skip if another goroutine in this process already claimed this port
		if !usedPorts[port] {
			usedPorts[port] = true
			return port
		}
	}
	t.Fatal("harness: could not allocate a unique free port after 20 attempts")
	return 0
}

// freePort removes a port from the tracking set when its server shuts down.
func freePort(port int) {
	usedPortsMu.Lock()
	delete(usedPorts, port)
	usedPortsMu.Unlock()
}

// harness-level description of one node.
type NodeConfig struct {
	Port        int
	Role        string // "leader" or "follower"
	LeaderAddr  string // set for followers only; e.g. "127.0.0.1:7001"
	MaxMemoryMB int64  // keep small in tests (64MB is enough)
}

// wraps a running GoCache server and exposes test-friendly controls.
type NodeHandle struct {
	t      testing.TB // testing.TB covers both *testing.T and *testing.B
	cfg    NodeConfig
	addr   string // "127.0.0.1:<port>"
	stopFn func() // calls the real server's Stop
	alive  atomic.Bool
}

// returns the "host:port" this node is listening on.
func (n *NodeHandle) Addr() string { return n.addr }

// returns the numeric port.
func (n *NodeHandle) Port() int { return n.cfg.Port }

// returns "leader" or "follower".
func (n *NodeHandle) Role() string { return n.cfg.Role }

// reports whether this node is still running.
func (n *NodeHandle) IsAlive() bool { return n.alive.Load() }

// shuts the node down cleanly and releases its port.
func (n *NodeHandle) Stop() {
	// CompareAndSwap ensures only the first caller to Stop actually acts.
	if n.alive.CompareAndSwap(true, false) {
		n.t.Logf("[harness] stopping %s node on :%d", n.cfg.Role, n.cfg.Port)
		n.stopFn()
		freePort(n.cfg.Port)
		n.t.Logf("[harness] %s node on :%d stopped", n.cfg.Role, n.cfg.Port)
	}
}

// stops the node without test logging — used by ChaosMonkey to simulate  an ungraceful crash rather than a clean shutdown. 
func (n *NodeHandle) Kill() {
	if n.alive.CompareAndSwap(true, false) {
		n.stopFn()
		freePort(n.cfg.Port)
	}
}

// manages a set of GoCache nodes for one test or benchmark.
type TestHarness struct {
	t     testing.TB // testing.TB covers both *testing.T and *testing.B
	nodes []*NodeHandle
	mu    sync.Mutex
}

// creates a harness for the given test or benchmark. 
func New(t testing.TB) *TestHarness {
	t.Helper()
	h := &TestHarness{t: t}
	// t.Cleanup runs after the test AND all subtests complete — more correct than defer inside the test body, which runs before subtests finish.
	t.Cleanup(h.stopAll)
	return h
}

// starts one leader node and blocks until it is ready to accept connections.
func (h *TestHarness) StartLeader() *NodeHandle {
	h.t.Helper()
	return h.startNode(NodeConfig{
		Port:        getFreePort(h.t),
		Role:        "leader",
		MaxMemoryMB: 64,
	})
}

// starts a follower that replicates from leader. blocks until the follower has connected to the leader and is ready.
func (h *TestHarness) StartFollower(leader *NodeHandle) *NodeHandle {
	h.t.Helper()
	return h.startNode(NodeConfig{
		Port:        getFreePort(h.t),
		Role:        "follower",
		LeaderAddr:  leader.Addr(),
		MaxMemoryMB: 64,
	})
}

// starts a leader plus numFollowers followers. returns all nodes with nodes[0] always being the leader.
func (h *TestHarness) StartCluster(numFollowers int) []*NodeHandle {
	h.t.Helper()
	leader := h.StartLeader()
	nodes := []*NodeHandle{leader}
	for i := 0; i < numFollowers; i++ {
		nodes = append(nodes, h.StartFollower(leader))
	}
	return nodes
}

// client creates a test client connected to node. the connection closes automatically when the test ends.
func (h *TestHarness) Client(node *NodeHandle) *Client {
	h.t.Helper()
	return NewClient(h.t, node.Addr())
}

// startNode is the single place where the harness creates real servers.
func (h *TestHarness) startNode(cfg NodeConfig) *NodeHandle {
	h.t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	h.t.Logf("[harness] starting %s node on %s", cfg.Role, addr)

	serverCfg := config.DefaultConfig()
	serverCfg.Port = cfg.Port
	serverCfg.Role = cfg.Role
	serverCfg.LeaderAddr = cfg.LeaderAddr
	serverCfg.MaxCacheSize = int(cfg.MaxMemoryMB * 1024 * 1024)
	// files that are cleaned up automatically after the test finishes.
	serverCfg.AOFFileName = h.t.TempDir() + "/test.aof"
	serverCfg.SnapshotFileName = h.t.TempDir() + "/test.snap"

	srv := server.New(serverCfg)

	errCh := make(chan struct{}, 1)
	go func() {
		srv.Start()
		errCh <- struct{}{} // signal that Start() has returned
	}()

	// above to exit, giving deferred closes (AOF, listener) time to finish.
	stopFn := func() {
		srv.Stop()
		<-errCh // drain so the goroutine exits cleanly
	}


	node := &NodeHandle{
		t:      h.t,
		cfg:    cfg,
		addr:   addr,
		stopFn: stopFn,
	}
	node.alive.Store(true)

	// block until the server TCP port actually accepts connections.
	if err := waitForReady(addr, 5*time.Second); err != nil {
		node.Kill()
		h.t.Fatalf("startNode: %s node on :%d never became ready: %v", cfg.Role, cfg.Port, err)
	}

	h.mu.Lock()
	h.nodes = append(h.nodes, node)
	h.mu.Unlock()

	h.t.Logf("[harness] %s node on %s is ready", cfg.Role, addr)
	return node
}

// stopAll shuts down all nodes in reverse start order (followers before leader).
func (h *TestHarness) stopAll() {
	h.mu.Lock()
	nodes := make([]*NodeHandle, len(h.nodes))
	copy(nodes, h.nodes)
	h.mu.Unlock()

	for i := len(nodes) - 1; i >= 0; i-- {
		nodes[i].Stop()
	}
}

// waitForReady dials addr repeatedly until a TCP connection succeeds or timeout is reached. 
func waitForReady(addr string, timeout time.Duration) error {
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
			delay *= 2 // double each round, cap at 100ms
		}
	}
	return fmt.Errorf("server at %s not ready after %s", addr, timeout)
}

// Client is a minimal RESP client for writing test assertions.
type Client struct {
	t    testing.TB // testing.TB covers both *testing.T and *testing.B
	conn net.Conn
	r    *bufio.Reader
}

// dials addr and returns a connected Client.
func NewClient(t testing.TB, addr string) *Client {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("harness.NewClient: dial %s: %v", addr, err)
	}
	c := &Client{t: t, conn: conn, r: bufio.NewReader(conn)}
	t.Cleanup(func() { conn.Close() })
	return c
}

// sends SET key value and returns the server's response.
func (c *Client) Set(key, value string) string {
	c.t.Helper()
	return c.Do("SET", key, value)
}

// sends SET key value EX ttlSeconds.
func (c *Client) SetEx(key, value string, ttlSeconds int) string {
	c.t.Helper()
	return c.Do("SET", key, value, "EX", fmt.Sprintf("%d", ttlSeconds))
}

// sends GET key and returns the value, or "" if the key does not exist.
func (c *Client) Get(key string) string {
	c.t.Helper()
	return c.Do("GET", key)
}

// sends DEL key.
func (c *Client) Del(key string) string {
	c.t.Helper()
	return c.Do("DEL", key)
}

// sends PING and returns the response (should be "PONG").
func (c *Client) Ping() string {
	c.t.Helper()
	return c.Do("PING")
}

// sends an arbitrary RESP command and returns the response as a string.
func (c *Client) Do(args ...string) string {
	c.t.Helper()

	// encode as RESP array: *<count>\r\n then $<len>\r\n<arg>\r\n per arg.
	var sb strings.Builder
	fmt.Fprintf(&sb, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&sb, "$%d\r\n%s\r\n", len(a), a)
	}

	// write deadline so a hung server causes a clear timeout, not an infinite hang.
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprint(c.conn, sb.String()); err != nil {
		c.t.Fatalf("harness.Client.Do(%v): write: %v", args, err)
	}

	return c.readResponse()
}

// readResponse reads exactly one RESP response from the server connection.
func (c *Client) readResponse() string {
	c.t.Helper()

	// read the first line which tells us the type and (for some types) the value.
	line, err := c.r.ReadString('\n')
	if err != nil {
		c.t.Fatalf("harness.Client: read response: %v", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 {
		return ""
	}

	switch line[0] {
	case '+': // simple string: +OK\r\n  →  "OK"
		return line[1:]

	case '-': // error: -ERR message\r\n  →  "ERR message"
		// return the error text so tests can assert "response starts with ERR".
		return line[1:]

	case ':': // integer: :42\r\n  →  "42"
		return line[1:]

	case '$': // bulk string: $6\r\nfoobar\r\n
		if line == "$-1" {
			// null bulk string means the key was not found — return empty string.
			return ""
		}
		var n int
		if _, err := fmt.Sscanf(line[1:], "%d", &n); err != nil {
			c.t.Fatalf("harness.Client: bad bulk string length in %q: %v", line, err)
		}
		// read exactly n data bytes plus the trailing \r\n the server sends.
		buf := make([]byte, n+2)
		if _, err := c.r.Read(buf); err != nil {
			c.t.Fatalf("harness.Client: read bulk body: %v", err)
		}
		return string(buf[:n]) // strip the trailing \r\n before returning

	case '*': // array — return the raw line; tests that expect arrays parse it.
		return line
	}
	return line
}

// polls getter until it returns expected, or fails the test after timeout with a diff showing what was actually returned.
func EventuallyEqual(t testing.TB, timeout time.Duration, expected string, getter func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	delay := 5 * time.Millisecond
	for time.Now().Before(deadline) {
		if got := getter(); got == expected {
			return // success — exit immediately
		}
		time.Sleep(delay)
		if delay < 250*time.Millisecond {
			delay *= 2 // back off so we don't hammer the server
		}
	}
	// one final check produces a proper failure message with the actual value.
	if got := getter(); got != expected {
		t.Errorf("EventuallyEqual: timed out after %s\n  got:  %q\n  want: %q", timeout, got, expected)
	}
}

// polls condition until it returns true, or fails after timeout.
func EventuallyTrue(t testing.TB, timeout time.Duration, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	delay := 10 * time.Millisecond
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(delay)
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
	t.Errorf("EventuallyTrue: condition %q never became true after %s", description, timeout)
}

// ChaosMonkey randomly kills nodes from a pool on a timer, simulating ungraceful crashes. 
type ChaosMonkey struct {
	nodes    []*NodeHandle // full pool; index 0 is always protected
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup

	mu     sync.Mutex
	killed []*NodeHandle // nodes the monkey has killed so far
}

// creates a monkey that will randomly kill follower nodes every interval
func NewChaosMonkey(nodes []*NodeHandle, interval time.Duration) *ChaosMonkey {
	return &ChaosMonkey{
		nodes:    nodes,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// begins random node killing in a background goroutine.
func (m *ChaosMonkey) Start() {
	m.wg.Add(1)
	go m.run()
}

// halts the monkey and waits for its goroutine to exit cleanly.
func (m *ChaosMonkey) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// returns how many nodes the monkey has killed so far.
func (m *ChaosMonkey) KilledCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.killed)
}

func (m *ChaosMonkey) run() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.maybeKill()
		}
	}
}

// picks a random alive follower (never nodes[0]) and kills it.
func (m *ChaosMonkey) maybeKill() {
	// Build the list of alive followers (skip index 0 — protected leader).
	var candidates []*NodeHandle
	for _, n := range m.nodes[1:] {
		if n.IsAlive() {
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		return // nothing killable right now
	}

	victim := candidates[rand.Intn(len(candidates))]
	victim.Kill()

	m.mu.Lock()
	m.killed = append(m.killed, victim)
	m.mu.Unlock()
}
