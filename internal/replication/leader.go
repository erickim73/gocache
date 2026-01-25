package replication

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/pkg/protocol"
)

type Leader struct {
	cache     *cache.Cache
	followers []*FollowerConn // list of connected followers
	seqNum    int64           // current sequence number
	mu        sync.RWMutex
	listener  net.Listener
}

type FollowerConn struct {
	conn net.Conn // tcp connection to follower
	id   string   // follower's id
	mu sync.Mutex // protects conn.Write

	lastHeartbeat time.Time // when did we last hear from this follower
	heartbeatMu sync.RWMutex // protects lastHeartbeat
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
		cache:     cache,
		followers: make([]*FollowerConn, 0),
		seqNum:    0,
		listener:  listener,
	}

	return leader, nil
}

func (l *Leader) Start() error {
	// load defaults
	cfg := config.DefaultConfig()

	fmt.Printf("Leader replication server listening on port %d...\n", cfg.Port+1)

	// goroutine to start sending heart beats
	go l.sendHeartbeats()

	for {
		conn, err := l.listener.Accept()
		if err != nil {
			return fmt.Errorf("error accepting connection: %v", err)
		}

		go l.handleFollower(conn)
	}
}

func (l *Leader) Close() error {
	return l.listener.Close()
}

func (l *Leader) handleFollower(conn net.Conn) {
	defer conn.Close()

	// read sync request from follower
	reader := bufio.NewReader(conn)

	syncReq, err := DecodeSyncRequest(reader)
	if err != nil {
		fmt.Printf("Error decoding SYNC request: %v\n", err)
		return
	}

	// create writer for sending replicate commands
	writer := bufio.NewWriter(conn)

	// get current snapshot
	snapshot := l.cache.Snapshot()

	// iterate over snapshot and send replicate command
	for key, entry := range snapshot {
		// increment seq num for each item
		l.mu.Lock()
		l.seqNum++
		seqNum := l.seqNum
		l.mu.Unlock()

		// calculate remaining TTL
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

		// create a replicate command
		cmd := &ReplicateCommand{
			SeqNum:    seqNum,
			Operation: OpSet,
			Key:       key,
			Value:     entry.Value,
			TTL:       ttlSeconds,
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

	fmt.Printf("Sent snapshot to follower %s\n", syncReq.FollowerID)

	// add follower to tracked list
	f := l.addFollower(syncReq.FollowerID, conn)

	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			fmt.Printf("Follower %s disconnected\n", syncReq.FollowerID)
			l.removeFollower(syncReq.FollowerID)
			return
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			continue
		}

		command := resultSlice[0]

		switch command {
		case CmdHeartbeat:
			// update health timestamp for follower
			f.heartbeatMu.Lock()
			f.lastHeartbeat = time.Now()
			f.heartbeatMu.Unlock()
		}
	}
}

func (l *Leader) addFollower(id string, conn net.Conn) *FollowerConn {
	l.mu.Lock()
	defer l.mu.Unlock()

	follower := &FollowerConn{
		id:   id,
		conn: conn,
		lastHeartbeat: time.Now(),
	}
	l.followers = append(l.followers, follower)
	
	fmt.Printf("Added followers %s (total: %d)\n", id, len(l.followers))
	return follower
}

func (l *Leader) removeFollower(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for i, f := range l.followers {
		if f.id == id {
			// remove by swapping with last element and truncating
			l.followers[i] = l.followers[len(l.followers)-1]
			l.followers = l.followers[:len(l.followers)-1]
			fmt.Printf("Removed follower %s (remaining %d)\n", id, len(l.followers))
			return
		}
	}
}

func (l *Leader) Replicate(operation string, key string, value string, ttl int64) error {
	// increment sequence number
	l.mu.Lock()
	l.seqNum++
	seqNum := l.seqNum

	followersCopy := make([]*FollowerConn, len(l.followers))
	copy(followersCopy, l.followers)
	l.mu.Unlock()

	// create command
	cmd := &ReplicateCommand{
		SeqNum:    seqNum,
		Operation: operation,
		Key:       key,
		Value:     value,
		TTL:       ttl,
	}

	// encode
	encoded, err := EncodeReplicateCommand(cmd)
	if err != nil {
		return err
	}

	// send to all followers
	for _, follower := range followersCopy {
		follower.mu.Lock()
		_, err := follower.conn.Write(encoded)
		follower.mu.Unlock()

		if err != nil {
			fmt.Printf("Error sending to follower %s: %v\n", follower.id, err)
		}
	}

	return nil
}

func (l *Leader) sendHeartbeats() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	fmt.Println("Leader heartbeat sender started")

	leaderID := "leader"
	for range ticker.C {
		// snapshot seqNum + followers list
		l.mu.RLock()
		seq := l.seqNum
		followersCopy := make([]*FollowerConn, len(l.followers))
		copy(followersCopy, l.followers)
		l.mu.RUnlock()

		// build heartbeat command
		heartbeat := &HeartbeatCommand{
			SeqNum: seq,
			NodeID: leaderID, 
		}
		encoded, err := EncodeHeartbeatCommand(heartbeat)
		if err != nil {
			fmt.Printf("Failed to encode heartbeat: %v\n", err)
			continue
		}

		// send to each follower
		for _, follower := range followersCopy {
			follower.mu.Lock()
			_, err := follower.conn.Write(encoded)
			follower.mu.Unlock()

			if err != nil {
				fmt.Printf("Heartbeat write failed to follower %s: %v\n", follower.id, err)
			}
		}
	}
}
