package pubsub

import (
	"net"
	"sync"
	"testing"
	"time"
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

	// verify each channel has this subscriber
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

// tests publishing to a channel with no subscribers
func TestPublish_NoSubscribers(t *testing.T) {
	ps := NewPubSub()

	// publish to a channel that doesn't exist
	count := ps.Publish("nonexistent", "hello")

	// should return 0 (nobody received it)
	if count != 0 {
		t.Errorf("expected 0 recipients, got %d", count)
	}
}

// tests publishing to a channel with one subscriber
func TestPublish_SingleSubscriber(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// subscribe
	err := ps.Subscribe(conn, "news")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// give the sendMessages goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// publish a message
	count := ps.Publish("news", "Breaking news!")

	// should return 1 (one subscriber)
	if count != 1 {
		t.Errorf("expected 1 recipient, got %d", count)
	}

	// give time for message to be sent
	time.Sleep(50 * time.Millisecond)

	// Verify the message was written to the connection
	if len(conn.written) == 0 {
		t.Error("expected message to be written to connection")
	}
}

// tests publishing to multiple subscribers
func TestPublish_MultipleSubscribers(t *testing.T) {
	ps := NewPubSub()
	conn1 := newMockConn("conn1")
	conn2 := newMockConn("conn2")
	conn3 := newMockConn("conn3")

	// all subscribe to the same channel
	connections := []*mockConn{conn1, conn2, conn3}
	for _, conn := range connections {
		err := ps.Subscribe(conn, "news")
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}
	}

	// give goroutines time to start
	time.Sleep(10 * time.Millisecond)

	// publish a message
	count := ps.Publish("news", "Important update")

	// should return 3 (all subscribers)
	if count != 3 {
		t.Errorf("expected 3 recipients, got %d", count)
	}

	// give time for messages to be sent
	time.Sleep(50 * time.Millisecond)

	// verify all connections received the message
	for _, conn := range connections {
		if len(conn.written) == 0 {
			t.Errorf("connection %s did not receive message", conn.id)
		}
	}
}

// tests that publishing only goes to the right channel
func TestPublish_SelectiveChannels(t *testing.T) {
	ps := NewPubSub()
	newsConn := newMockConn("newsConn")
	sportsConn := newMockConn("sportsConn")
	bothConn := newMockConn("bothConn")

	// subscribe to different channels
	ps.Subscribe(newsConn, "news")
	ps.Subscribe(sportsConn, "sports")
	ps.Subscribe(bothConn, "news")
	ps.Subscribe(bothConn, "sports")

	// give goroutines time to start
	time.Sleep(10 * time.Millisecond)

	// publish to news channel
	count := ps.Publish("news", "News update")

	// should reach 2 subscribers (newsConn and bothConn)
	if count != 2 {
		t.Errorf("expected 2 recipients for news, got %d", count)
	}

	// publish to sports channel
	count = ps.Publish("sports", "Sports update")

	// should reach 2 subscribers (sportsConn and bothConn)
	if count != 2 {
		t.Errorf("expected 2 recipients for sports, got %d", count)
	}
}

// tests unsubscribing from one channel
func TestUnsubscribe_SingleChannel(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// subscribe to two channels
	ps.Subscribe(conn, "news")
	ps.Subscribe(conn, "sports")

	// unsubscribe from one
	err := ps.Unsubscribe(conn, "news")
	if err != nil {
		t.Fatalf("Unsubscribe failed: %v", err)
	}

	// verify subscriber count
	if count := ps.GetSubscriberCount("news"); count != 0 {
		t.Errorf("expected 0 subscribers to news, got %d", count)
	}

	if count := ps.GetSubscriberCount("sports"); count != 1 {
		t.Errorf("expected 1 subscriber to sports, got %d", count)
	}

	// verify subscribed channels
	channels := ps.GetSubscribedChannels(conn)
	if len(channels) != 1 {
		t.Errorf("expected 1 subscribed channel, got %d", len(channels))
	}
}

// tests unsubscribing from all channels
func TestUnsubscribe_AllChannels(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// subscribe to two channels
	ps.Subscribe(conn, "news")
	ps.Subscribe(conn, "sports")

	// unsubscribe from both
	ps.Unsubscribe(conn, "news")
	ps.Unsubscribe(conn, "sports")

	// verify the connection is completely cleaned up
	if _, exists := ps.subscriptions[conn]; exists {
		t.Error("connection should be removed from subscriptions map")
	}

	// verify channels are cleaned up
	if count := ps.GetSubscriberCount("news"); count != 0 {
		t.Errorf("expected 0 subscribers to news, got %d", count)
	}
	if count := ps.GetSubscriberCount("sports"); count != 0 {
		t.Errorf("expected 0 subscribers to sports, got %d", count)
	}
}

// tests unsubscribing from a channel you're not subscribed to
func TestUnsubscribe_NotSubscribed(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// try to unsubscribe without subscribing first
	err := ps.Unsubscribe(conn, "news")
	if err == nil {
		t.Error("expected error when unsubscribing from non-existent subscription")
	}
}

// tests removing a connection completely
func TestRemoveConnection(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// subscribe to multiple channels
	ps.Subscribe(conn, "news")
	ps.Subscribe(conn, "sports")
	ps.Subscribe(conn, "weather")

	// remove the connection
	ps.RemoveConnection(conn)

	// verify the connection is completely cleaned up
	if _, exists := ps.subscriptions[conn]; exists {
		t.Error("connection should be removed from subscriptions map")
	}

	// verify all channels are cleaned up
	channels := []string{"news", "sports", "weather"}
	for _, channel := range channels {
		if count := ps.GetSubscriberCount(channel); count != 0 {
			t.Errorf("channel %s: expected 0 subscribers, got %d", channel, count)
		}
	}
}

// tests that removing one connection doesn't affect others
func TestRemoveConnection_MultipleSubscribers(t *testing.T) {
	ps := NewPubSub()
	conn1 := newMockConn("conn1")
	conn2 := newMockConn("conn2")

	// both subscribe to the same channel
	ps.Subscribe(conn1, "news")
	ps.Subscribe(conn2, "news")

	// remove one connection
	ps.RemoveConnection(conn1)

	// verify the other connection is still subscribed
	if count := ps.GetSubscriberCount("news"); count != 1 {
		t.Errorf("expected 1 subscriber remaining, got %d", count)
	}

	// verify conn2 is still tracked
	channels := ps.GetSubscribedChannels(conn2)
	if len(channels) != 1 || channels[0] != "news" {
		t.Error("conn2 should still be subscribed to news")
	}
}

// tests retrieving all active channels
func TestGetChannels(t *testing.T) {
	ps := NewPubSub()
	conn1 := newMockConn("conn1")
	conn2 := newMockConn("conn2")

	// subscribe to various channels
	ps.Subscribe(conn1, "news")
	ps.Subscribe(conn1, "sports")
	ps.Subscribe(conn2, "weather")

	// get all channels
	channels := ps.GetChannels()

	// should have 3 channels
	if len(channels) != 3 {
		t.Errorf("expected 3 channels, got %d", len(channels))
	}

	// verify all expected channels are present
	expectedChannels := map[string]bool{"news": true, "sports": true, "weather": true}
	for _, channel := range channels {
		if !expectedChannels[channel] {
			t.Errorf("unexpected channel: %s", channel)
		}
	}
}

// tests concurrent subscriptions (race condition check)
func TestConcurrentSubscribe(t *testing.T) {
	ps := NewPubSub()

	// create 100 connections
	var wg sync.WaitGroup
	numConnections := 100

	for i := 0; i < numConnections; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn := newMockConn(string(rune(id)))
			ps.Subscribe(conn, "news")
		}(i)
	}

	wg.Wait()

	// verify all connections are subscribed
	count := ps.GetSubscriberCount("news")
	if count != numConnections {
		t.Errorf("expected %d subscribers, got %d", numConnections, count)
	}
}

// tests concurrent publishing (race condition check)
func TestConcurrentPublish(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// subscribe one connection
	ps.Subscribe(conn, "news")
	time.Sleep(10 * time.Millisecond)

	// publish from multiple goroutines concurrently
	var wg sync.WaitGroup
	numPublishers := 100

	for i := 0; i < numPublishers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ps.Publish("news", "message")
		}(i)
	}

	wg.Wait()
	// Just verify no panics occurred
	// The actual message delivery is tested in other tests
}

// tests behavior when a subscriber's buffer is full
func TestSlowSubscriber(t *testing.T) {
	ps := NewPubSub()
	conn := newMockConn("conn1")

	// Subscribe
	ps.Subscribe(conn, "news")

	// Don't start the sendMessages goroutine properly - simulate a stuck subscriber by filling the buffer beyond capacity

	// Publish more messages than buffer size (100)
	for i := 0; i < 150; i++ {
		ps.Publish("news", "message")
	}

	// The publish should not block
	// Some messages will be dropped due to full buffer
	// This test mainly verifies no deadlock occurs
}
