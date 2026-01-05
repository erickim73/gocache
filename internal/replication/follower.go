package replication

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/erickim73/gocache/internal/cache"
)

type Follower struct {
	cache      *cache.Cache
	leaderAddr string        // host:replPort
	id         string        // follower id
	conn       net.Conn      // tcp connection to leader
	lastSeqNum int64         // next sequence to assign
	mu         sync.RWMutex  // protects conn 	
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
	return err
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
	}
}

func (f *Follower) closeConn() {
	f.mu.Lock()
	conn := f.conn
	defer f.mu.Unlock()

	if conn != nil {
		_ = f.conn.Close()
		f.conn = nil
	}
}