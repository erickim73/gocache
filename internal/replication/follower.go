package replication

import (
	"fmt"
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

}