package replication

import (
	"net"
	"sync"

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
	follower := &Follower{
		cache: cache,
		leaderAddr: leaderAddr,
		lastSeqNum: 0,
		id: id,
	}

	return follower, nil
} 