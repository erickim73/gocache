package pubsub

import (
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/erickim73/gocache/pkg/protocol"
)

// carry both channel name and message content
type Message struct {
	Channel string // which channel this message was published to
	Content string // actual message content
}

// represents a single client connection that is subscribed to one or more channels
type Subscriber struct {
	conn     net.Conn      // tcp connection to this subscriber
	messages chan Message  // buffered channel for queueing messages to send
	done     chan struct{} // signal channel to stop sendMessages goroutine
}

// manages all publish/subscribe operations in cache system
type PubSub struct {
	// map for O(1) subscriber lookup. ensures exactly one subscriber per connection
	connectionToSubscriber map[net.Conn]*Subscriber

	// map channel names to set of connections subscribed to that channel
	// example: subscribers["news"] = {conn1: subscriber1, conn2: subscriber2}
	subscribers map[string]map[net.Conn]*Subscriber

	// map connections to the set of channels they're subscribed to
	// example: subscriptions[conn1] = {"news": true, "sports": true}
	subscriptions map[net.Conn]map[string]bool

	// protects both maps from concurrent actions
	mu sync.RWMutex
}

// creates and initializes a new pubsub manager
func NewPubSub() *PubSub {
	return &PubSub{
		connectionToSubscriber: make(map[net.Conn]*Subscriber),
		subscribers:            make(map[string]map[net.Conn]*Subscriber),
		subscriptions:          make(map[net.Conn]map[string]bool),
	}
}

// adds a connection to a channel's subscriber list
func (ps *PubSub) Subscribe(conn net.Conn, channel string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// check if connection has a subscriber struct
	subscriber, exists := ps.connectionToSubscriber[conn]

	if !exists {
		// create a subscriber struct since this is a new connection
		subscriber = &Subscriber{
			conn:     conn,
			messages: make(chan Message, 100),
			done:     make(chan struct{}),
		}

		// store in direct lookup map
		ps.connectionToSubscriber[conn] = subscriber

		// initialize channel set for this connection
		ps.subscriptions[conn] = make(map[string]bool)

		// start goroutine that sends messages to this subscriber
		go subscriber.sendMessages()

		slog.Debug("Created new subscriber",
			"remote_addr", conn.RemoteAddr(),
		)
	} else {
		// already subscribed to other channels. check if already subscribed to this channel
		if ps.subscriptions[conn][channel] {
			slog.Debug("Already subscribed to channel",
				"channel", channel,
				"remote_addr", conn.RemoteAddr(),
			)
			return nil // Already subscribed, nothing to do
		}
	}

	// add this channel to connection's subscription set
	ps.subscriptions[conn][channel] = true

	// add connection to channel's subscriber set
	if ps.subscribers[channel] == nil {
		ps.subscribers[channel] = make(map[net.Conn]*Subscriber)
	}
	ps.subscribers[channel][conn] = subscriber

	slog.Debug("Subscribed to channel",
		"channel", channel,
		"total_channels", len(ps.subscriptions[conn]),
		"remote_addr", conn.RemoteAddr(),
	)

	return nil
}

// goroutine that continuously reads from subscriber's message channel and writes them to network connection
func (s *Subscriber) sendMessages() {
	for {
		select {
		case msg, ok := <-s.messages:
			if !ok {
				// channel closed, subscriber has been removed
				slog.Debug("Message channel closed, exiting sendMessages goroutine")
				return
			}

			// encode message in resp format
			encoded := protocol.EncodePubSubMessage(msg.Channel, msg.Content)

			// write to connection
			_, err := s.conn.Write([]byte(encoded))
			if err != nil {
				// write failed, connection is dead
				slog.Debug("Failed to write message, connection appears dead",
					"error", err,
				)
				return
			}

		case <-s.done:
			// signal to stop this goroutine
			slog.Debug("Received done signal, exiting sendMessages goroutine")
			return
		}
	}
}

// sends a message to all subscribers of a channel. returns the number of subscribers who received the message
func (ps *PubSub) Publish(channel string, message string) int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	// get all subscribers for this channel
	subscribers, exists := ps.subscribers[channel]
	if !exists || len(subscribers) == 0 {
		return 0
	}

	count := 0

	// create message struct with channel information
	msg := Message{
		Channel: channel,
		Content: message,
	}

	// send to each subscriber
	for _, subscriber := range subscribers {
		select {
		case subscriber.messages <- msg:
			// message successfully queued in subscriber's buffer
			count++
		default:
			// subscriber's channel is full. drop message
			slog.Warn("Subscriber buffer full, dropping message",
				"channel", channel,
				"remote_addr", subscriber.conn.RemoteAddr(),
				"buffer_size", cap(subscriber.messages),
			)
		}
	}

	return count
}

// removes a connection from a channel's subscriber list
func (ps *PubSub) Unsubscribe(conn net.Conn, channel string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// check if this connection has any subscriptions
	channelSet, exists := ps.subscriptions[conn]
	if !exists {
		slog.Debug("Connection not subscribed to any channels",
			"remote_addr", conn.RemoteAddr(),
		)
		return fmt.Errorf("connection not subscribed to any channels")
	}

	// check if subscribed to this specific channel
	if !channelSet[channel] {
		slog.Debug("Connection not subscribed to channel",
			"channel", channel,
			"remote_addr", conn.RemoteAddr(),
		)
		return fmt.Errorf("connection not subscribed to channel: %s", channel)
	}

	// remove channel from connection's subscription set
	delete(channelSet, channel)

	// remove connection from channel's subscriber set
	if subscribers, ok := ps.subscribers[channel]; ok {
		delete(subscribers, conn)

		// if channel has no more subscribers, clean up channel
		if len(subscribers) == 0 {
			delete(ps.subscribers, channel)
		}
	}

	// check if connection is still subscribed to any other channel
	if len(channelSet) == 0 {
		ps.cleanupConnection(conn)
	}

	slog.Debug("Unsubscribed from channel",
		"channel", channel,
		"remaining_channels", len(channelSet),
		"remote_addr", conn.RemoteAddr(),
	)

	return nil
}

// forcefully removes a connection and all its subscriptions
func (ps *PubSub) RemoveConnection(conn net.Conn) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// get subscriber reference before removing from channels
	subscriber, exists := ps.connectionToSubscriber[conn]
	if !exists {
		// Connection was never subscribed, nothing to clean up
		slog.Debug("RemoveConnection called on non-subscribed connection",
			"remote_addr", conn.RemoteAddr(),
		)
		return
	}

	// find al channels this connection is subscribed to
	channelSet, exists := ps.subscriptions[conn]
	if !exists {
		slog.Warn("Inconsistent state: subscriber exists but no subscriptions",
			"remote_addr", conn.RemoteAddr(),
		)
		delete(ps.connectionToSubscriber, conn)
		return
	}

	// unsubscribe from each channel
	for channel := range channelSet {
		// remove from channel's subscriber set
		if subscribers, ok := ps.subscribers[channel]; ok {
			delete(subscribers, conn)

			// clean up empty channel entires
			if len(subscribers) == 0 {
				delete(ps.subscribers, channel)
			}
		}
	}

	// cleanup subscriber
	close(subscriber.messages)

	// signal done channel
	close(subscriber.done)

	// remove tracking maps
	delete(ps.subscriptions, conn)
	delete(ps.connectionToSubscriber, conn)

	slog.Debug("Removed connection from all pub/sub channels",
		"remote_addr", conn.RemoteAddr(),
		"channels_count", len(channelSet),
	)
}

// removes a connection's Subscriber struct and stops its goroutine
func (ps *PubSub) cleanupConnection(conn net.Conn) {
	subscriber, exists := ps.connectionToSubscriber[conn]
	if !exists {
		// already cleaned up or never existed
		slog.Debug("cleanupConnection: subscriber not found",
			"remote_addr", conn.RemoteAddr(),
		)
		return
	}

	// close channels to stop the sendMessages goroutine
	close(subscriber.messages)
	close(subscriber.done)

	// remove from tracking maps
	delete(ps.subscriptions, conn)
	delete(ps.connectionToSubscriber, conn)

	slog.Debug("Cleaned up connection",
		"remote_addr", conn.RemoteAddr(),
	)
}

// returns number of subscribers for a given channel
func (ps *PubSub) GetSubscriberCount(channel string) int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if subscribers, exists := ps.subscribers[channel]; exists {
		return len(subscribers)
	}

	return 0
}

// returns all active channels that have at least one subscriber
func (ps *PubSub) GetChannels() []string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	channels := make([]string, 0, len(ps.subscribers))
	for channel := range ps.subscribers {
		channels = append(channels, channel)
	}

	return channels
}

// returns all channels that a connection is subscribed to
func (ps *PubSub) GetSubscribedChannels(conn net.Conn) []string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	channelSet, exists := ps.subscriptions[conn]
	if !exists {
		return []string{}
	}

	channels := make([]string, 0, len(channelSet))
	for channel := range channelSet {
		channels = append(channels, channel)
	}

	return channels
}
