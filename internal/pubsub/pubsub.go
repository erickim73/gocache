package pubsub

import (
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/erickim73/gocache/pkg/protocol"
)

// represents a single client connection that is subscribed to one or more channels
type Subscriber struct {
	conn net.Conn // tcp connection to this subscriber
	messages chan string // buffered channel for queueing messages to send
	done chan struct{} // signal channel to stop sendMessages goroutine
}

// manages all publish/subscribe operations in cache system
type PubSub struct {
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
		subscribers: make(map[string]map[net.Conn]*Subscriber),
		subscriptions: make(map[net.Conn]map[string]bool),
	}
}

// adds a connection to a channel's subscriber list
func (ps *PubSub) Subscribe(conn net.Conn, channel string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// check if connection has a subscriber struct
	var subscriber *Subscriber
	channelSet, exists := ps.subscriptions[conn]

	if !exists {
		// create a subscriber struct since this is a new connection
		subscriber = &Subscriber{
			conn: conn,
			messages: make(chan string, 100),
			done: make(chan struct{}),
		}
		
		// initialize channel set for this connection
		ps.subscriptions[conn] = make(map[string]bool)

		// start goroutine that sends messages to this subscriber
		go subscriber.sendMessages()
	} else {
		// connection already subscribed to other channels. check if they're subscribed to this channel
		if channelSet[channel] {
			return nil
		}

		// find existing subscriber struct
		for existingChannel := range channelSet {
			if subs, ok := ps.subscribers[existingChannel]; ok {
				if sub, ok := subs[conn]; ok {
					subscriber = sub
					break
				}
			}
		}

		// if no subscriber, create new one
		if subscriber == nil {
			subscriber = &Subscriber{
				conn: conn,
				messages: make(chan string, 100),
				done: make(chan struct{}),
			}
			go subscriber.sendMessages()
		}
	}

	// add this channel to connection's subscription set
	ps.subscriptions[conn][channel] = true

	// add connection to channel's subscriber set
	if ps.subscribers[channel] == nil {
		ps.subscribers[channel] = make(map[net.Conn]*Subscriber)
	}
	ps.subscribers[channel][conn] = subscriber

	return nil
}

// goroutine that continuously reads from subscriber's message channel and writes them to network connection
func (s *Subscriber) sendMessages() {
	for {
		select {
		case msg, ok := <-s.messages:
			if !ok {
				// channel closed, subscriber has been removed
				return
			}

			// encode message in resp format
			encoded := protocol.EncodeSimpleString(msg)

			// write to connection
			_, err := s.conn.Write([]byte(encoded))
			if err != nil {
				// write failed, connection is dead
				return
			}
		
		case <-s.done:
			// signal to stop this goroutine
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

	// send to each subscriber
	for _, subscriber := range subscribers {
		select {
		case subscriber.messages <- message:
			// message successfully queued in subscriber's buffer
			count++
		default:
			// subscriber's channel is full. drop message
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
		slog.Error("Connection not subscribed to any channels")
		return fmt.Errorf("connection not subscribed to any channels")
	}

	// check if subscribed to this specific channel
	if !channelSet[channel] {
		slog.Error("Connection not subscribed to channel", "channel", channel)
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

	return nil
}

// forcefully removes a connection and all its subscriptions
func (ps *PubSub) RemoveConnection (conn net.Conn) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// find al channels this connection is subscribed to
	channelSet, exists := ps.subscriptions[conn]
	if !exists {
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

	// clean up connection's subscriber struct
	ps.cleanupConnection(conn)
}

// removes a connection's Subscriber struct and stops its goroutine
func (ps *PubSub) cleanupConnection(conn net.Conn) {
	delete(ps.subscriptions, conn)

	// find subscriber 
	for _, subscribers := range ps.subscribers {
		if subscriber, ok := subscribers[conn]; ok {
			// close message channel
			close(subscriber.messages)

			// signal done channel for shutdown
			close(subscriber.done)

			break
		}
	}
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