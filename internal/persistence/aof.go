package persistence

import (
	"sync"
	"os"
	"fmt"
	"time"
	// "github.com/erickim73/gocache/pkg/protocol/encoder"
)

// Determines how often data is appended to AOF
// 0 = always, 1 = every sec, 2 = no
type SyncPolicy int

type AOF struct {
	file * os.File
	mu sync.Mutex   // read write lock
	policy SyncPolicy
}

func NewAOF (fileName string, policy SyncPolicy) (*AOF, error) {
	// open file for read/write, create if it doesn't exist
	file, err := os.OpenFile(fileName, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	return &AOF{file: file, policy: policy}, nil
}

func (aof *AOF) append(data string) error {
	aof.mu.Lock()
	defer aof.mu.Unlock()

	line := data + "\n"
	_, err := aof.file.Write([]byte(line))
	if err != nil {
		return err
	}

	if aof.policy == 0 {
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

	for range ticker.C {
		aof.mu.Lock()
		aof.file.Sync()
		aof.mu.Unlock()
	}
}