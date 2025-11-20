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
	mu sync.Mutex   // read write lock
	policy SyncPolicy
	cache *cache.Cache
	done chan struct{}
	opsSinceRewrite int64
	rewriteThreshold int64
	rewriting bool
}

type Operation struct {
	Type string // SET or DEL
	Key string
	Value string
	TTL int64 // ttl in sec; 0 means it lives forever
}

func NewAOF (fileName string, policy SyncPolicy, cache *cache.Cache, rewriteThreshold int64) (*AOF, error) {
	// open file for read/write, create if it doesn't exist
	file, err := os.OpenFile(fileName, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	aof := &AOF {
		file: file,
		fileName: fileName,
		policy: policy,
		cache: cache,
		done: make(chan struct{}),
		opsSinceRewrite: 0,
		rewriteThreshold: rewriteThreshold,
		rewriting: false,
	}

	if policy == SyncEverySecond {
		go aof.periodicSync()
	}

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