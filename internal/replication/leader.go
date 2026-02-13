package replication

import (
	"bufio"
	"fmt"
	"log/slog"
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
	replPort  int
}

type FollowerConn struct {
	conn net.Conn   // tcp connection to follower
	id   string     // follower's id
	mu   sync.Mutex // protects conn.Write

	lastHeartbeat time.Time    // when did we last hear from this follower
	heartbeatMu   sync.RWMutex // protects lastHeartbeat
}

func NewLeader(cache *cache.Cache, aof *persistence.AOF, replPort int) (*Leader, error) {
	// if replPOrt is 0, use default
	if replPort == 0 {
		cfg := config.DefaultConfig()
		replPort = cfg.Port + 1
	}


	// create a tcp listener on a port
	address := fmt.Sprintf("0.0.0.0:%d", replPort)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		slog.Error("Error creating listener", "error", err, "port", replPort)
		return nil, err
	}

	leader := &Leader{
		cache:     cache,
		followers: make([]*FollowerConn, 0),
		seqNum:    0,
		listener:  listener,
		replPort:  replPort,
	}

	return leader, nil
}

func (l *Leader) Start() error {
	slog.Info("Leader replication server listening", "port", l.replPort)

	// goroutine to send and monitor heart beats
	go l.sendHeartbeats()
	go l.monitorFollowerHealth()

	for {
		conn, err := l.listener.Accept()
		if err != nil {
			slog.Error("Error accepting connection", "error", err)
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
		slog.Error("Error decoding SYNC request", "error", err)
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
			slog.Error("Error encoding replicate command", "error", err, "key", key)
			return
		}

		_, err = writer.Write(encoded)
		if err != nil {
			slog.Error("Error sending snapshot", "error", err, "key", key)
			return
		}
	}

	// flush writer to ensure all data is sent
	writer.Flush()

	// send SYNCEND to signal end of snapshot
	l.mu.RLock()
	finalSeqNum := l.seqNum
	l.mu.RUnlock()

	syncEndMsg := EncodeSyncEnd(finalSeqNum)
	_, err = writer.Write(syncEndMsg)
	if err != nil {
		slog.Error("Error sending SYNCEND", "error", err, "follower_id", syncReq.FollowerID)
		return
	}

	err = writer.Flush()
	if err != nil {
		slog.Error("Error flushing SYNCEND", "error", err, "follower_id", syncReq.FollowerID)
		return
	}

	slog.Info("Sent snapshot to follower", "follower_id", syncReq.FollowerID)

	// add follower to tracked list
	f := l.addFollower(syncReq.FollowerID, conn)

	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			slog.Info("Follower disconnected", "follower_id", syncReq.FollowerID)
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
		id:            id,
		conn:          conn,
		lastHeartbeat: time.Now(),
	}
	l.followers = append(l.followers, follower)

	slog.Info("Added follower", "follower_id", id, "total_followers", len(l.followers))
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
			slog.Info("Removed follower", "follower_id", id, "remaining_followers", len(l.followers))
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
			slog.Error("Error sending to follower", "follower_id", follower.id, "error", err)
		}
	}

	return nil
}

func (l *Leader) sendHeartbeats() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	slog.Info("Leader heartbeat sender started")

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
			slog.Error("Failed to encode heartbeat", "error", err)
			continue
		}

		// send to each follower
		for _, follower := range followersCopy {
			follower.mu.Lock()
			_, err := follower.conn.Write(encoded)
			follower.mu.Unlock()

			if err != nil {
				slog.Error("Heartbeat write failed to follower", "follower_id", follower.id, "error", err)
			}
		}
	}
}

func (l *Leader) monitorFollowerHealth() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := 15 * time.Second

	for range ticker.C {
		// copy followers list
		l.mu.RLock()
		followersCopy := make([]*FollowerConn, len(l.followers))
		copy(followersCopy, l.followers)
		l.mu.RUnlock()

		// check each followers last heartbeat
		for _, follower := range followersCopy {
			follower.heartbeatMu.RLock()
			last := follower.lastHeartbeat
			follower.heartbeatMu.RUnlock()

			// if never set, skip
			if last.IsZero() {
				continue
			}

			if time.Since(last) > timeout {
				slog.Warn("Follower is dead (no heartbeat)", "follower_id", follower.id, "time_since_heartbeat", time.Since(last))

				// close connection
				follower.mu.Lock()
				_ = follower.conn.Close()
				follower.mu.Unlock()
			}
		}
	}

}
