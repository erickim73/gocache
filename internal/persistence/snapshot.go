package persistence

import (
	"time"
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

	// iterate over snapshot and write to tempFile
	for key, entry := range snapshot {
		if !entry.ExpiresAt.IsZero() {
			ttl := entry.ExpiresAt.Sub(time.Now())
			if ttl < 0 {
				continue // skip expired items
			}
		}

	}


}
