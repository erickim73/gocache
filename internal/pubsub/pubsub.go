package pubsub

import (
	"net"
	"sync"
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

