package persistence

import (
	"sync"
	"os"
	"fmt"
	"time"
)

// Determines how often data is appended to AOF
type SyncPolicy int

const (
	SyncAlways SyncPolicy = 0
	SyncEverySecond SyncPolicy = 1
	SyncNo SyncPolicy = 2
)

type AOF struct {
	file * os.File
	mu sync.Mutex   // read write lock
	policy SyncPolicy
	done chan struct{}
}

func NewAOF (fileName string, policy SyncPolicy) (*AOF, error) {
	// open file for read/write, create if it doesn't exist
	file, err := os.OpenFile(fileName, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	aof := &AOF {
		file: file,
		policy: policy,
		done: make(chan struct{}),
	}

	if policy == SyncEverySecond {
		go aof.periodicSync()
	}

	return aof, nil
}

func (aof *AOF) Append(data string) error {
	aof.mu.Lock()
	defer aof.mu.Unlock()

	line := data + "\n"
	_, err := aof.file.Write([]byte(line))
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