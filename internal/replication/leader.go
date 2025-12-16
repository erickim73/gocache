package replication

import (
	"bufio"
	"fmt"
	"net"
	"sync"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/persistence"
)

type Leader struct {
	cache *cache.Cache
	followers []*FollowerConn // list of connected followers
	seqNum int64 // current sequence number
	mu sync.RWMutex
	listener net.Listener
}

type FollowerConn struct {
	conn net.Conn // tcp connection to follower
	id string // follower's id
}

func NewLeader(cache *cache.Cache, aof *persistence.AOF) (*Leader, error) {
	// load defaults
	cfg := config.DefaultConfig()

	port := cfg.Port + 1

	// create a tcp listener on a port 
	address := fmt.Sprintf("0.0.0.0:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Printf("Error creating listener: %v", err)
		return nil, err
	}

	leader := &Leader{
		cache: cache,
		followers: make([]*FollowerConn, 0),
		seqNum: 0,
		listener: listener,
	}

	return leader, nil
}

func (l *Leader) Start() error {
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			return fmt.Errorf("error accepting connection: %v", err)
		}

		go l.handleFollower(conn)
	}

	return nil
}

func (l *Leader) handleFollower(conn net.Conn) {
	defer conn.Close()

	// read from client
	reader := bufio.NewReader(conn)
	for {
		decoded, err := DecodeSyncRequest()
	}
	
}