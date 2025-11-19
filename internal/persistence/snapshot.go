package persistence

import (
	"strconv"
	"time"
	"fmt"

	"github.com/erickim73/gocache/pkg/protocol"
)



func (aof *AOF) rewriteAOF () (error) {
	// create a new temp aof file
	tempName := aof.fileName + "_temp"
	tempFile, err := NewAOF(tempName, aof.policy, aof.cache)
	if err != nil {
		return err
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
		ttl := entry.ExpiresAt.Sub(time.Now())
		ttlSeconds := strconv.Itoa(int(ttl.Seconds()))
		aofCommand := protocol.EncodeArray([]string{"SET", key, entry.Value, ttlSeconds})
		err := tempFile.Append(aofCommand)
		if err != nil {
			fmt.Printf("Failed to write to temp AOF file: %v\n", err)
		}
	}


}
