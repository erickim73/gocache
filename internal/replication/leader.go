package replication

import (
	"sync"
	"net"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
)

type Leader struct {
	cache *cache.Cache
	aof *persistence.AOF
	followers []*Follower // list of connected followers
	seqNum int64 // current sequence number
	mu sync.RWMutex
}

type Follower struct {
	conn net.Conn // tcp connection to follower
	id string // follower's id
}

func NewLeader(cache *cache.Cache, aof *persistence.AOF, port int) (*Leader, error) {
	return &Leader{
		cache: cache,
		aof: aof,
		followers: []*Follower{},
		seqNum: 0,
	}, nil
}