package persistence

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/erickim73/gocache/pkg/protocol"
)

func (aof *AOF) createSnapshot() error {
	// create a temp snapshot file
	tempName := aof.snapshotName + ".temp.rdb"
	tempFile, err := os.OpenFile(tempName, os.O_CREATE | os.O_WRONLY | os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	success := false
	defer func() {
		if tempFile != nil { 
			// check if already closed
			tempFile.Close()
		}
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
			ttl = time.Until(entry.ExpiresAt)

			if ttl < 0 {
				continue // skip expired items
			}
		}
		
		ttlSeconds := strconv.Itoa(int(ttl.Seconds()))
		aofCommand := protocol.EncodeArray([]string{"SET", key, entry.Value, ttlSeconds})

		// write directly to file
		_, err := tempFile.Write([]byte(aofCommand))
		if err != nil {
			return fmt.Errorf("failed to write to temp AOF file: %v", err)
		}
	}

	// ensure all writes in tempFile are flushed to disk
	err = tempFile.Sync()
	if err != nil {
		return fmt.Errorf("fsync failed: %v", err)
	}

	err = tempFile.Close()
	if err != nil {
		return fmt.Errorf("closing tempfile failed: %v", err)
	}

	// lock during file swap
	aof.mu.Lock()
	defer aof.mu.Unlock()
	
	// rename temp file to original
	err = os.Rename(tempName, aof.snapshotName)
	if err != nil {
		return fmt.Errorf("renaming file failed: %v", err)
	}

	// close current aof
	aof.file.Close()

	// empty aof
	err = os.Truncate(aof.fileName, 0)
	if err != nil {
		return fmt.Errorf("trncating AOF failed: %v", err)
	}

	// reopen aof file so future append calls work
	newFile, err := os.OpenFile(aof.fileName, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to reopen new aof: %v", err)
	}
	
	aof.file = newFile

	// mark rewrite successful
	success = true
	return nil
}

func (aof *AOF) loadSnapshot() error {
	snapshot, err := os.Open(aof.snapshotName)
	if err != nil {
		return fmt.Errorf("failed to open snapshot: %v", err)
	}
	defer snapshot.Close()

	reader := bufio.NewReader(snapshot)

	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			if err == io.EOF {
				break // reached end of file
			}
			fmt.Println("Skipping corrupted entry: ", err)
			continue
		}

		// type assert based on what Parse() returns
		partsInterface, ok := result.([]interface{})
		if !ok || len(partsInterface) == 0 {
			continue
		}

		// convert []interface{} to []string
		parts := make([]string, len(partsInterface))
		for i, v := range partsInterface{
			parts[i], ok = v.(string)
			if !ok {
				fmt.Printf("element %d is not a string\n", i)
				break
			}
		}

		if parts[0] == "SET" && len(parts) >= 3 {
			ttl := time.Duration(0)
			if len(parts) >= 4 {
				ttlSeconds, err := strconv.ParseInt(parts[3], 10, 64)
				if err == nil {
					ttl = time.Duration(ttlSeconds) * time.Second
				}
			}
			aof.cache.Set(parts[1], parts[2], ttl)
		}
	}

	return nil
}