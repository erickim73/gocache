package replication

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/pkg/protocol"
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

func (l *Leader) Close() error {
	return l.listener.Close()
}

func (l *Leader) handleFollower(conn net.Conn) {
	defer conn.Close()

	// read sync request from follower
	reader := bufio.NewReader(conn)
	value, err := protocol.Parse(reader)
	if err != nil {
		fmt.Printf("Error parsing SYNC request: %v\n", err)
		return
	}

	// create writer for sending replicate commands
	writer := bufio.NewWriter(conn)

	// get current snapshot
	snapshot := l.cache.Snapshot()

	// iterate over snapshot and send repliate command
	for key, entry := range snapshot {
		// increment seq num for each item
		l.mu.Lock()
		l.seqNum++
		seqNum := l.seqNum
		l.mu.Unlock()

		ttl := time.Duration(0)
		// no expiration, TTL = 0
		if entry.ExpiresAt.IsZero() {
			ttl = 0
		} else {
			// has an expiration
			ttl = time.Until(entry.ExpiresAt)

			if ttl < 0 {
				continue // skip expired items
			}
		}

		ttlSeconds := int64(ttl.Seconds())

		cmd := &ReplicateCommand{
			SeqNum: seqNum,
			Operation: OpSet,
			Key: key,
			Value: entry.Value,
			TTL: ttlSeconds,
		}

		// encode and send
		encoded, err := EncodeReplicateCommand(cmd)
		if err != nil {
			fmt.Printf("Error encoding: %v\n", err)
			return
		}

		_, err = writer.Write(encoded)
		if err != nil {
			fmt.Printf("Error sending snapshot: %v\n", err)
			return
		}
	}	

	// flush writer to ensure all data is sent
	writer.Flush()

	fmt.Printf("Sent snapshot to follower %s\n", followerID)

	
}