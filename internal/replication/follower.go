package replication

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/pkg/protocol"
)

type Follower struct {
	cache      *cache.Cache
	aof        *persistence.AOF
	leaderAddr string       // host:replPort
	id         string       // follower id
	conn       net.Conn     // tcp connection to leader
	reader     *bufio.Reader // follower uses this reader
	lastSeqNum int64        // next sequence to assign
	mu         sync.RWMutex // protects conn and lastSeqNum

	lastHeartbeat time.Time    // when did follower last hear from leader
	heartbeatMu   sync.RWMutex // protects lastHeartbeat and isLeaderAlive
	isLeaderAlive bool         // is leader currently alive

	clusterNodes []config.NodeInfo // all nodes in cluster (empty if not in cluster mode)
	myPriority   int               // my priority (0 if not in cluster mode)
	myReplPort   int               // replication port for when node becomes leader

	promoted   bool         // set to true when promoted to leader
	promotedMu sync.RWMutex // protects promoted flag

	nodeStateUpdater NodeStateUpdater  

	stopCh chan struct{} // Server.Stop() can signal the follower loop to exit
}

// interface for updating node state after promotion
type NodeStateUpdater interface {
	SetRole(role string)
	SetLeader(leader *Leader)
}

func NewFollower(cache *cache.Cache, aof *persistence.AOF, leaderAddr string, id string, clusterNodes []config.NodeInfo, myPriority int, myReplPort int, nodeStateUpdater NodeStateUpdater) (*Follower, error) {
	if cache == nil {
		slog.Error("Cache instance cannot be nil")
		return nil, fmt.Errorf("cache instance cannot be nil")
	}
	if leaderAddr == "" {
		slog.Error("Leader address cannot be empty")
		return nil, fmt.Errorf("leader address cannot be empty")
	}
	if id == "" {
		slog.Error("Follower id cannot be empty")
		return nil, fmt.Errorf("follower id cannot be empty")
	}

	follower := &Follower{
		cache:        cache,
		aof:          aof,
		leaderAddr:   leaderAddr,
		lastSeqNum:   0,
		id:           id,
		clusterNodes: clusterNodes,
		myPriority:   myPriority,
		myReplPort:   myReplPort,
		nodeStateUpdater: nodeStateUpdater,
		stopCh: make(chan struct{}),
	}

	return follower, nil
}

func (f *Follower) Start() error {
	backoff := 200 * time.Millisecond
	maxBackoff := 5 * time.Second
	failedAttempts := 0  // track failed attempts
	maxAttemptsBeforeElection := 7 // after 7 fails, trigger election

	for {
		// check stopCh so Server.Stop() can terminate this loop cleanly
		select {
		case <-f.stopCh:
			slog.Info("Follower stopping (shutdown signal received)", "follower_id", f.id)
			return nil
		default:
		}

		// check if follower has been promoted
		f.promotedMu.RLock()
		if f.promoted {
			f.promotedMu.RUnlock()
			slog.Info("Stopping follower loop (now leader)", "follower_id", f.id)
			return nil  // exit the loop
		}
		f.promotedMu.RUnlock()

		err := f.connectToLeader()
		if err != nil {
			slog.Warn("Connect to leader failed, retrying", "follower_id", f.id, "error", err, "backoff", backoff)

			failedAttempts++

			if failedAttempts >= maxAttemptsBeforeElection && len(f.clusterNodes) > 0 {
				slog.Warn("Failed to connect multiple times, triggering election", "follower_id", f.id, "failed_attempts", failedAttempts)
                go f.startElection()
                failedAttempts = 0  // Reset counter
			}

			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// reset backoff after successful connection
		backoff = 200 * time.Millisecond
		failedAttempts = 0

		// send SYNC request
		err = f.sendSyncRequest()
		if err != nil {
			slog.Warn("SYNC request failed, reconnecting", "follower_id", f.id, "error", err)
			f.closeConn()
			continue
		}

		f.mu.RLock()
		conn := f.conn
		f.mu.RUnlock()

		// initialize heartbeat state
		f.heartbeatMu.Lock()
		if f.lastHeartbeat.IsZero() {
			f.lastHeartbeat = time.Now()
		}
		f.isLeaderAlive = true
		f.heartbeatMu.Unlock()

		slog.Info("Started goroutine for heartbeats")
		go f.sendHeartbeats(conn)
		go f.monitorLeaderHealth(conn)

		// read and apply replication stream
		err = f.processReplicationStream()
		if err != nil {
			slog.Warn("Replication stream failed, reconnecting", "follower_id", f.id, "error", err)
			f.closeConn()
			continue
		}
	}
}

// signals the follower's Start() loop to exit cleanly
func (f *Follower) Stop() {
	select {
	case <- f.stopCh:
		// already closed
	default:
		close(f.stopCh)
	}
	f.closeConn() // unblock any blocking read in processReplicationStream
}

func (f *Follower) connectToLeader() error {
	conn, err := net.Dial("tcp", f.leaderAddr)
	if err != nil {
		return err
	}

	f.mu.Lock()
	f.conn = conn
	f.reader = bufio.NewReader(conn)
	f.mu.Unlock()
	return nil
}

func (f *Follower) sendSyncRequest() error {
	f.mu.RLock()
	conn := f.conn
	lastSeqNum := f.lastSeqNum
	reader := f.reader
	f.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("no connection")
	}

	req := &SyncRequest{
		FollowerID: f.id,
		LastSeqNum: lastSeqNum,
	}

	encoded, err := EncodeSyncRequest(req)
	if err != nil {
		return err
	}

	f.mu.Lock()
	_, err = conn.Write(encoded)
	f.mu.Unlock()

	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			return err
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			slog.Error("Parse result is not a slice", "follower_id", f.id)
			return fmt.Errorf("Error: result is not a slice")
		}
		command := resultSlice[0]

		f.heartbeatMu.Lock()
		f.lastHeartbeat = time.Now()
		f.heartbeatMu.Unlock()

		if command == "REPLICATE" {
			// resultSlice = [REPLICATE, seqNum, operation, key, value?, ttl?]

			if len(resultSlice) < 4 {
				slog.Error("Invalid REPLICATE command - too few elements", "follower_id", f.id, "length", len(resultSlice))
				return fmt.Errorf("invalid REPLICATE command")
			}

			// extract seqNum
			var seqNum int64
			seqNum, ok := ParseInt64(resultSlice[1])
			if !ok {
				slog.Error("Invalid sequence number", "follower_id", f.id)
				return fmt.Errorf("invalid sequence number")
			}

			// extract operation
			operation, ok := resultSlice[2].(string)
			if !ok {
				slog.Error("Operation must be a string", "follower_id", f.id)
				return fmt.Errorf("operation must be a string")
			}

			// extract key
			key, ok := resultSlice[3].(string)
			if !ok {
				slog.Error("Key must be a string", "follower_id", f.id)
				return fmt.Errorf("key must be a string")
			}

			// handle SET vs DEL
			if operation == OpSet {
				if len(resultSlice) != 6 {
					slog.Error("SET requires 6 elements", "follower_id", f.id, "length", len(resultSlice))
					return fmt.Errorf("SET requires 6 elements")
				}

				value, ok := resultSlice[4].(string)
				if !ok {
					slog.Error("Value must be a string", "follower_id", f.id)
					return fmt.Errorf("value must be a string")
				}

				ttl, ok := ParseInt64(resultSlice[5])
				if !ok {
					slog.Error("TTL must be an integer", "follower_id", f.id)
					return fmt.Errorf("ttl must be an integer")
				}

				// apply to cache
				ttlDuration := time.Duration(ttl) * time.Second
				f.cache.Set(key, value, ttlDuration)
			} else if operation == OpDelete {
				if len(resultSlice) != 4 {
					slog.Error("DELETE requires 4 elements", "follower_id", f.id, "length", len(resultSlice))
					return fmt.Errorf("DELETE requires 4 elements")
				}

				f.cache.Delete(key)
			} else {
				slog.Error("Unknown operation", "follower_id", f.id, "operation", operation)
				return fmt.Errorf("unknown operation: %s", operation)
			}

			// update lastSeqNum
			f.mu.Lock()
			if seqNum > f.lastSeqNum {
				f.lastSeqNum = seqNum
			}
			f.mu.Unlock()
		} else if command == "SYNCEND" {
			seqNum, ok := DecodeSyncEnd(resultSlice)
			if !ok {
				slog.Error("Failed to decode SYNCEND", "follower_id", f.id)
				return fmt.Errorf("failed to decode SYNCEND")
			}

			// update final sequence number
			f.mu.Lock()
			if seqNum > f.lastSeqNum {
				f.lastSeqNum = seqNum
			}
			f.mu.Unlock()

			break
		}
	}

	return nil
}

func (f *Follower) processReplicationStream() error {
	f.mu.Lock()
	conn := f.conn
	reader := f.reader
	f.mu.Unlock()

	if conn == nil {
		slog.Error("No connection to leader", "follower_id", f.id)
		return fmt.Errorf("no connection to leader")
	}

	slog.Info("Starting replication stream processing", "follower_id", f.id)

	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			return err
		}

		f.heartbeatMu.Lock()
		f.lastHeartbeat = time.Now()
		f.heartbeatMu.Unlock()

		resultSlice, ok := result.([]interface{})
		if !ok {
			slog.Error("Parse result is not a slice in replication stream", "follower_id", f.id)
			return fmt.Errorf("Error: result is not a slice")
		}
		command := resultSlice[0]

		if command == "REPLICATE" {
			// resultSlice = [REPLICATE, seqNum, operation, key, value?, ttl?]

			if len(resultSlice) < 4 {
				slog.Error("Invalid REPLICATE command - too few elements", "follower_id", f.id, "length", len(resultSlice))
				return fmt.Errorf("invalid REPLICATE command")
			}

			// extract seqNum
			var seqNum int64
			seqNum, ok := ParseInt64(resultSlice[1])
			if !ok {
				slog.Error("Invalid sequence number", "follower_id", f.id)
				return fmt.Errorf("invalid sequence number")
			}

			// extract operation
			operation, ok := resultSlice[2].(string)
			if !ok {
				slog.Error("Operation must be a string", "follower_id", f.id)
				return fmt.Errorf("operation must be a string")
			}

			// extract key
			key, ok := resultSlice[3].(string)
			if !ok {
				slog.Error("Key must be a string", "follower_id", f.id)
				return fmt.Errorf("key must be a string")
			}

			// handle SET vs DEL
			if operation == OpSet {
				if len(resultSlice) != 6 {
					slog.Error("SET requires 6 elements", "follower_id", f.id, "length", len(resultSlice))
					return fmt.Errorf("SET requires 6 elements")
				}

				value, ok := resultSlice[4].(string)
				if !ok {
					slog.Error("Value must be a string", "follower_id", f.id)
					return fmt.Errorf("value must be a string")
				}

				ttl, ok := ParseInt64(resultSlice[5])
				if !ok {
					slog.Error("TTL must be an integer", "follower_id", f.id)
					return fmt.Errorf("ttl must be an integer")
				}

				// apply to cache
				ttlDuration := time.Duration(ttl) * time.Second
				f.cache.Set(key, value, ttlDuration)
			} else if operation == OpDelete {
				if len(resultSlice) != 4 {
					slog.Error("DELETE requires 4 elements", "follower_id", f.id, "length", len(resultSlice))
					return fmt.Errorf("DELETE requires 4 elements")
				}

				f.cache.Delete(key)
			} else {
				slog.Error("Unknown operation", "follower_id", f.id, "operation", operation)
				return fmt.Errorf("unknown operation: %s", operation)
			}

			// update lastSeqNum
			f.mu.Lock()
			if seqNum > f.lastSeqNum {
				f.lastSeqNum = seqNum
			}
			f.mu.Unlock()
		} else if command == CmdHeartbeat {
			continue
		}

	}
}

func (f *Follower) closeConn() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.conn != nil {
		_ = f.conn.Close()
		f.conn = nil
		f.reader = nil
	}
}

func (f *Follower) sendHeartbeats(conn net.Conn) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// if this goroutine's conn is no longer the active one, stop
		f.mu.RLock()
		current := f.conn
		seq := f.lastSeqNum
		f.mu.RUnlock()

		if current == nil || current != conn {
			return
		}

		hb := &HeartbeatCommand{
			SeqNum: seq,
			NodeID: f.id,
		}

		encoded, err := EncodeHeartbeatCommand(hb)
		if err != nil {
			continue
		}

		f.mu.Lock()
		_, err = conn.Write(encoded)
		f.mu.Unlock()

		if err != nil {
			return
		}
	}
}

func (f *Follower) monitorLeaderHealth(conn net.Conn) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := 15 * time.Second

	for range ticker.C {
		// stop goroutine if it's no longer responsible for conn
		f.mu.RLock()
		current := f.conn
		f.mu.RUnlock()

		f.promotedMu.RLock()
		promoted := f.promoted
		f.promotedMu.RUnlock()
		if promoted {
			return
		}

		f.heartbeatMu.RLock()
		last := f.lastHeartbeat
		alive := f.isLeaderAlive
		f.heartbeatMu.RUnlock()

		// if we've never heard from leader, skip
		if last.IsZero() {
			continue
		}

		if time.Since(last) > timeout {
			if alive {
				// alive -> dead
				slog.Warn("Leader is dead (no heartbeat)", "follower_id", f.id, "time_since_last_heartbeat", time.Since(last))
			}

			f.heartbeatMu.Lock()
			f.isLeaderAlive = false
			f.heartbeatMu.Unlock()

			if current != nil {
				f.closeConn()
			}

			// force election
			go f.startElection()
			return
		} else {
			// leader is healthy
			f.heartbeatMu.Lock()
			f.isLeaderAlive = true
			f.heartbeatMu.Unlock()
		}
	}
}

func (f *Follower) startElection() {
	slog.Info("Starting election", "follower_id", f.id, "priority", f.myPriority)

	// self promote if not in cluster config
	if len(f.clusterNodes) == 0 || f.myPriority == 0 {
		slog.Info("No cluster config/priority, skipping election", "follower_id", f.id)
	} else {
		// priority-based election: higher priority = shorter wait
		maxPriority := 0
		for _, n := range f.clusterNodes {
			if n.Priority > maxPriority {
				maxPriority = n.Priority
			}
		}

		waitSteps := maxPriority - f.myPriority
		if waitSteps < 0 {
			waitSteps = 0
		}

		waitTime := time.Duration(waitSteps) * time.Second
        slog.Info("Waiting before attempting leadership", "follower_id", f.id, "wait_time", waitTime)
		time.Sleep(waitTime)

		newLeaderAddr := f.findNewLeader()
        if newLeaderAddr != "" {
            slog.Info("Detected existing leader, aborting election", "follower_id", f.id)
            f.mu.Lock()
            f.leaderAddr = newLeaderAddr
            f.mu.Unlock()
            return
        }
	}

	// promote self to leader
	slog.Info("Promoting self to leader", "follower_id", f.id)
	f.closeConn()

	// when myReplPort is 0 (simple mode), ask OS for a free port instead of using hardcoded default
	replPort := f.myReplPort
	if replPort == 0 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			slog.Error("Cannot find free port for promotion", "follower_id", f.id, "error", err)
			return
		}
		replPort = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		slog.Info("Assigned dynamic replication port for promotion", "follower_id", f.id, "port", replPort)
	}

	// create leader with existing aof
	leader, err := NewLeader(f.cache, f.aof, replPort)
	if err != nil {
		slog.Error("Error creating leader", "error", err)
		
		f.promotedMu.Lock()
		f.promoted = true
		f.promotedMu.Unlock()
		return
	}

	// update node state using interface
	if f.nodeStateUpdater != nil {
		f.nodeStateUpdater.SetRole("leader")
		f.nodeStateUpdater.SetLeader(leader)
	}

	// start leader's replication server
	go leader.Start()

	// set promoted flag to stop follower loop
	f.promotedMu.Lock()
	f.promoted = true
	f.promotedMu.Unlock()

	slog.Info("Follower is now leader", "follower_id", f.id)
}

func (f *Follower) findNewLeader() string {
	timeout := 300 * time.Millisecond

	for _, node := range f.clusterNodes {
		if node.ID == f.id {
			continue
		}

		addr := net.JoinHostPort(node.Host, strconv.Itoa(node.ReplPort))

		conn, err := net.DialTimeout("tcp", addr, timeout)
		if err != nil {
			continue
		}

		// try sending sync request; real reader will accept it
		req := &SyncRequest{
			FollowerID: f.id,
			LastSeqNum: f.lastSeqNum,
		}
		encoded, err := EncodeSyncRequest(req)
		if err == nil {
			_, werr := conn.Write(encoded)
			_ = conn.Close()
			if werr == nil {
				// someone is listening and accepts SYNC
				slog.Info("Found existing leader", "follower_id", f.id, "leader_addr", addr)
				return addr
			}
		} else {
			_ = conn.Close()
		}
	}
	
	// no leader found
	return ""
}
