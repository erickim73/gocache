package pubsub

import (
	"net"
	"sync"
	"testing"
)

// mock network connection for testing
type mockConn struct {
	net.Conn
	id string // unique identifier for debugging
	closed bool
	written []byte // track what was written to this connection
	mu sync.Mutex
}

// write implements net.Conn.Write for our mock
func (m *mockConn) Write(b []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if m.closed {
		return 0, net.ErrClosed
	}

	m.written = append(m.written, b...)
	return len(b), nil
}

// implements net.Conn.Close for our mock
func (m *mockConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.closed = true
	return nil
}

// creates a new mock connection with a unique ID
func newMockConn(id string) *mockConn {
	return &mockConn{
		id: id,
		written: make([]byte, 0),
	}
}

// tests that PubSub is initialized correctly
func TestNewPubSub(t *testing.T) {
	ps := NewPubSub()

	// verify maps are initialized
	if ps.subscribers == nil {
		t.Error("subscribers map should be initialized")
	}

	if ps.subscriptions == nil {
		t.Error("subscriptions map should be initialized")
	}

	// verify maps are empty
	if len(ps.subscribers) != 0 {
		t.Errorf("expected 0 subscribers, got %d", len(ps.subscribers))
	}

	if len(ps.subscriptions) != 0 {
		t.Errorf("expected 0 subscriptions, got %d", len(ps.subscriptions))
	}
}

// tests subscribing a new connection
func TestSubscribe_NewConnection(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// subscribe to a channel
	err := ps.Subscribe(conn, "news")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// verify connection is tracked in subscriptions map
	if channelSet, exists := ps.subscriptions[conn]; !exists {
		t.Error("connection not found in subscriptions map")
	} else if !channelSet["news"] {
		t.Errorf("connection not subscribed to 'news' channel")
	}

	// verify connection is tracked in subscribers map
	if subscribers, exists := ps.subscribers["news"]; !exists {
		t.Error("'news' channel not found in subscribers map")
	} else if _, exists := subscribers[conn]; !exists {
		t.Error("connection not found in 'news' channel subscribers")
	}

	// verify subscriber count
	count := ps.GetSubscriberCount("news")
	if count != 1 {
		t.Errorf("expected 1 subscriber, got %d", count)
	}
}

// tests a connection subscribing to multiple channels
func TestSubscribe_MultipleChannels(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// subscribe to multiple channels
	channels := []string{"news", "sports", "weather"}
	for _, channel := range channels {
		err := ps.Subscribe(conn, channel)
		if err != nil {
			t.Fatalf("Subscribe to %s failed: %v", channel, err)
		}
	}

	// verify connection is subscribed to all channels
	subscribedChannels := ps.GetSubscribedChannels(conn)
	if len(subscribedChannels) != 3 {
		t.Errorf("expected 3 subscribed channels, got %d", len(subscribedChannels))
	}

	// verify each channel has this usbscriber
	for _, channel := range channels {
		count := ps.GetSubscriberCount(channel)
		if count != 1 {
			t.Errorf("channel %s: expected 1 subscriber, got %d", channel, count)
		}
	}
}

// tests subscribing to the same channel twice (should be idempotent)
func TestSubscribe_DuplicateSubscription(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// subscribe twice to the same channel
	err := ps.Subscribe(conn, "news")
	if err != nil {
		t.Fatalf("First subscribe failed: %v", err)
	}

	err = ps.Subscribe(conn, "news")
	if err != nil {
		t.Fatalf("Second subscribe failed: %v", err)
	}

	// should still have only 1 subscriber
	count := ps.GetSubscriberCount("news")
	if count != 1 {
		t.Errorf("expected 1 subscriber (idempotent), got %d", count)
	}
}

//  tests multiple connections subscribing to the same channel
func TestSubscribe_MultipleConnections(t *testing.T) {
	ps := NewPubSub()
	conn1 := newMockConn("conn1")
	conn2 := newMockConn("conn2")
	conn3 := newMockConn("conn3")

	// all subscribe to the same channel
	connections := []*mockConn{conn1, conn2, conn3}
	for _, conn := range connections {
		err := ps.Subscribe(conn, "news")
		if err != nil {
			t.Fatalf("Subscribe failed for %s: %v", conn.id, err)
		}
	}

	// verify subscriber count
	count := ps.GetSubscriberCount("news")
	if count != 3 {
		t.Errorf("expected 3 subscribers, got %d", count)
	}
}