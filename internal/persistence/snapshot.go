package persistence

import (
	"strconv"
	"time"
	"fmt"
	"os"

	"github.com/erickim73/gocache/pkg/protocol"
)



func (aof *AOF) rewriteAOF () (error) {
	// create a new temp aof file
	tempName := aof.fileName + "_temp"
	tempFile, err := NewAOF(tempName, aof.policy, aof.cache)
	if err != nil {
		return err
	}

	defer tempFile.file.Close()

	success := false
	defer func() {
		if !success {
			os.Remove(tempName)
		}
	}()

	// create a snapshot of the cache
	snapshot := aof.cache.Snapshot()

	// iterate over snapshot and write to tempFile
	for key, entry := range snapshot {
		ttl := time.Duration(0)

		// no expiration, TTL = 0
		if entry.ExpiresAt.IsZero() {
			ttl = 0
		} else {
			// has an expiration
			ttl = entry.ExpiresAt.Sub(time.Now())

			if ttl < 0 {
				continue // skip expired items
			}
		}
		
		ttlSeconds := strconv.Itoa(int(ttl.Seconds()))
		aofCommand := protocol.EncodeArray([]string{"SET", key, entry.Value, ttlSeconds})
		err := tempFile.Append(aofCommand)
		if err != nil {
			return fmt.Errorf("failed to write to temp AOF file: %v", err)
		}
	}

	// ensure all writes in tempFile are flushed to disk
	err = tempFile.file.Sync()
	if err != nil {
		return fmt.Errorf("fsync failed: %v", err)
	}

	// close temp file
	err = tempFile.file.Close()
	if err != nil {
		return fmt.Errorf("closing file failed: %v", err)
	}

	// rename temp file to original
	err = os.Rename(tempName, aof.fileName)
	if err != nil {
		return fmt.Errorf("renaming file failed: %v", err)
	}
	
	// reopen file so future writes append to new aof
	newFile, err := os.OpenFile(aof.fileName, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to reopen new aof: %v", err)
	}
	
	aof.file = newFile

	success = true
	return nil
}
