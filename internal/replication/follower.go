package replication

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/pkg/protocol"

)

type Follower struct {
	cache      *cache.Cache
	leaderAddr string        // host:replPort
	id         string        // follower id
	conn       net.Conn      // tcp connection to leader
	lastSeqNum int64         // next sequence to assign
	mu         sync.RWMutex  // protects conn 	

	lastHeartbeat time.Time  // when did follower last hear from leader
	heartbeatMu sync.RWMutex // protects lastHeartbeat
	isLeaderAlive bool       // is leader currently alive
}	

func NewFollower(cache *cache.Cache, leaderAddr string, id string) (*Follower, error) {
	if cache == nil {
		return nil, fmt.Errorf("cache instance cannot be nil")
	}
	if leaderAddr == "" {
		return nil, fmt.Errorf("leader address cannot be empty")
	}
	if id == "" {
		return nil, fmt.Errorf("follower id cannot be empty")
	}

	follower := &Follower{
		cache: cache,
		leaderAddr: leaderAddr,
		lastSeqNum: 0,
		id: id,
	}

	return follower, nil
} 

func (f *Follower) Start() error {
	backoff := 200 * time.Millisecond
	maxBackoff := 5 * time.Second

	for {
		err := f.connectToLeader()
		if err != nil {
			fmt.Printf("follower %s connect failed: %v; retrying in %v\n", f.id, err, backoff)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// reset backoff after successful connection
		backoff = 200 * time.Millisecond
		
		// send SYNC request
		err = f.sendSyncRequest()
		if err != nil {
			fmt.Printf("follower %s SYNC failed: %v; reconnecting\n", f.id, err)
			f.closeConn()
			continue
		}

		f.mu.RLock()
		conn := f.conn
		f.mu.Unlock()

		go f.sendHeartbeats(conn)

		// read and apply replication stream
		err = f.processReplicationStream()
		if err != nil {
			fmt.Printf("follower %s replication failed: %v; reconnecting\n", f.id, err)
			f.closeConn()
			continue
		}
	}
}

func (f *Follower) connectToLeader() error {
	conn, err := net.Dial("tcp", f.leaderAddr)
	if err != nil {
		return err
	}

	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	return nil
}

func (f *Follower) sendSyncRequest() error {
	f.mu.RLock()
	conn := f.conn
	lastSeqNum := f.lastSeqNum
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

	_, err = conn.Write(encoded)

	// create a reader
	reader := bufio.NewReader(conn)

	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			return err
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			return fmt.Errorf("Error: result is not a slice")
		}
		command := resultSlice[0]

		if command == "REPLICATE" {
			// resultSlice = [REPLICATE, seqNum, operation, key, value?, ttl?]

			if len(resultSlice) < 4 {
				return fmt.Errorf("invalid REPLICATE command")
			}

			// extract seqNum
			var seqNum int64
			seqNum, ok := ParseInt64(resultSlice[1])
			if !ok {
				return fmt.Errorf("invalid sequence number")
			}

			// extract operation
			operation, ok := resultSlice[2].(string)
			if !ok {
				return fmt.Errorf("operation must be a string")
			}

			// extract key
			key, ok := resultSlice[3].(string)
			if !ok {
				return fmt.Errorf("key must be a string")
			}

			// handle SET vs DEL
			if operation == OpSet {
				if len(resultSlice) != 6 {
					return fmt.Errorf("SET requires 6 elements")
				}

				value, ok := resultSlice[4].(string)
				if !ok {
					return fmt.Errorf("value must be a string")
				}

				ttl, ok := ParseInt64(resultSlice[5])
				if !ok {
					return fmt.Errorf("ttl must be an integer")
				}

				// apply to cache
				ttlDuration := time.Duration(ttl) * time.Second
				f.cache.Set(key, value, ttlDuration)	
			} else if operation == OpDelete {
				if len(resultSlice) != 4 {
					return fmt.Errorf("DELETE requires 4 elements")
				}

				f.cache.Delete(key)
			} else {
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
	f.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("no connection to leader")
	}
	
	// read from leader
	reader := bufio.NewReader(conn)
	
	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			return err
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			return fmt.Errorf("Error: result is not a slice")
		}
		command := resultSlice[0]

		if command == "REPLICATE" {
			// decode replicate command
			repCmd, err := DecodeReplicateCommand(reader)
			if err != nil {
				return err
			}

			// apply command
			switch repCmd.Operation {
			case OpSet:
				ttl := time.Duration(repCmd.TTL) * time.Second
				f.cache.Set(repCmd.Key, repCmd.Value, ttl)
			
			case OpDelete:
				f.cache.Delete(repCmd.Key)
			}

			// update lastSeqNum
			f.mu.Lock()
			if repCmd.SeqNum > f.lastSeqNum {
				f.lastSeqNum = repCmd.SeqNum
			}
			f.mu.Unlock()
		} else if command == CmdHeartbeat {
			f.lastHeartbeat = time.Now()
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
	}
}

func (f *Follower) sendHeartbeats(conn net.Conn) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		<- ticker.C // block until next tick arrives
		
		// if this goroutine's conn is no longer the active one, stop
		f.mu.Lock()
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

		f.heartbeatMu.Lock()
		_, err = conn.Write(encoded)
		f.heartbeatMu.Unlock()

		if err != nil {
			// connection is dead so close. 
			f.closeConn()
			return
		}
	}
}