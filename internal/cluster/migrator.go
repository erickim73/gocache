package cluster

import (
	"fmt"
	"sync"

	"github.com/erickim73/gocache/internal/cache"
)

// orchestrates data migration when nodes join or leave the cluster
// coordinates between the cache, hash ring, and network transfer
type Migrator struct {
	cache *cache.Cache // local cache containing data
	hashRing *HashRing // hash ring for routing decisions
	mu sync.Mutex // prevents concurrent migrations
}

// creates a new migration coordinator
func NewMigrator(cache *cache.Cache, hashRing *HashRing) *Migrator {
	return &Migrator{
		cache: cache,
		hashRing: hashRing,
	}
}

