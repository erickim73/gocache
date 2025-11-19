package persistence

import (
	"github.com/erickim73/gocache/internal/cache"
)



func (aof *AOF) rewriteAOF () (*AOF, error) {
	// create a new temp aof file
	tempName := aof.fileName + "_temp"
	tempFile, err := NewAOF(tempName, aof.policy, aof.cache)
	if err != nil {
		return nil, err
	}

	// create a snapshot of the cache
	snapshot := aof.cache.Snapshot()


}

