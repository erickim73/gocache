package persistence

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"
	
	"github.com/erickim73/gocache/pkg/protocol"
	"github.com/erickim73/gocache/internal/cache"
)

// Determines how often data is appended to AOF
type SyncPolicy int

const (
	SyncAlways SyncPolicy = 0
	SyncEverySecond SyncPolicy = 1
	SyncNo SyncPolicy = 2
)

type AOF struct {
	file *os.File
	fileName string
	snapshotName string
	mu sync.Mutex   // read write lock
	policy SyncPolicy
	cache *cache.Cache
	done chan struct{}
	growthFactor int64
	lastRewriteSize int64
	rewriting bool
}

type Operation struct {
	Type string // SET or DEL
	Key string
	Value string
	TTL int64 // ttl in sec; 0 means it lives forever
}

func AofExists(fileName string) bool {
	_, err := os.Stat(fileName)
	return !os.IsNotExist(err)
}

func NewAOF (fileName string, snapshotName string, policy SyncPolicy, cache *cache.Cache, growthFactor int64) (*AOF, error) {
	// open file for read/write, create if it doesn't exist
	file, err := os.OpenFile(fileName, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	aof := &AOF {
		file: file,
		fileName: fileName,
		snapshotName: snapshotName,
		policy: policy,
		cache: cache,
		done: make(chan struct{}),
		growthFactor: growthFactor,
		lastRewriteSize: 0,
		rewriting: false,
	}

	if policy == SyncEverySecond {
		go aof.periodicSync()
	}

	// start periodic snapshot goroutine
	go aof.checkSnapshotTrigger()

	return aof, nil
}

func (aof *AOF) Append(data string) error {
	aof.mu.Lock()
	defer aof.mu.Unlock()

	_, err := aof.file.Write([]byte(data))
	if err != nil {
		return err
	}

	if aof.policy == SyncAlways {
		// ensure durability
		err = aof.file.Sync()
		if err != nil {
			return fmt.Errorf("fsync failed: %v", err)
		}
	}

	// check ratio trigger
	aof.checkRewriteTrigger()

	return nil
}

// For SyncEverySecond Policy
func (aof *AOF) periodicSync() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select{
		case <- ticker.C:
			// sync
			aof.mu.Lock()
			aof.file.Sync()
			aof.mu.Unlock()
		case <- aof.done:
			// got stop signal
			return
		}
	}
}

func (aof *AOF) Close() error {
	if aof.policy == SyncEverySecond {
		close(aof.done)
	}

	return aof.file.Close()
}

// reads aof file, parse each line into operation structs, return slice of operations
func (aof *AOF) ReadOperations() ([]Operation, error) {
	file, err := os.Open(aof.fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	
	var operations []Operation

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

		op := Operation{}
		op.Type= parts[0]
		if op.Type == "SET" && len(parts) >= 3 {
			op.Key = parts[1]
			op.Value = parts[2]

			if len(parts) >= 4 {
				ttlSeconds, err := strconv.ParseInt(parts[3], 10, 64)
				if err == nil {
					op.TTL = ttlSeconds
				}
			}
		} else if op.Type == "DEL" && len(parts) >= 2 {
			op.Key = parts[1]
		}

		operations = append(operations, op)
	}

	return operations, nil
}


func (aof *AOF) rewriteAOF () error {
	// create a new temp aof file
	tempName := aof.fileName + "_temp"
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

	// save old file
	oldFile := aof.file

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

	// close old file
	oldFile.Close()

	// update baseline size
	info, _ := os.Stat(aof.fileName)
	aof.lastRewriteSize = info.Size() 

	// mark rewrite successful
	success = true
	return nil
}

func (aof *AOF) checkRewriteTrigger() {
	if aof.rewriting {
		return
	}

	// current aof file size
	info, err := aof.file.Stat()
	if err != nil {
		return 
	}
	currentSize := info.Size()

	// if first time, set baseline
	if aof.lastRewriteSize == 0 {
		aof.lastRewriteSize = currentSize
		return
	}

	// growth ratio
	growthRatio := float64(currentSize) / float64(aof.lastRewriteSize)

	
	
	if growthRatio > float64(aof.growthFactor) {
		// set flag and launch rewrite
		aof.rewriting = true
		
		go func() {
			err := aof.rewriteAOF()

			aof.mu.Lock()
			aof.rewriting = false
			aof.mu.Unlock()

			if err != nil {
				fmt.Println("rewrite failed: ", err)
			}

		}()
	}
}
