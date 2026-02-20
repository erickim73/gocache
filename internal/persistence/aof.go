package persistence

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/pkg/protocol"
)

// Determines how often data is appended to AOF
type SyncPolicy int

const (
	SyncAlways      SyncPolicy = 0
	SyncEverySecond SyncPolicy = 1
	SyncNo          SyncPolicy = 2
)

type AOF struct {
	file            *os.File
	fileName        string
	snapshotName    string
	mu              sync.Mutex // read write lock
	policy          SyncPolicy
	cache           *cache.Cache
	done            chan struct{}
	wg              sync.WaitGroup
	growthFactor    int64
	lastRewriteSize int64
	rewriting       bool
	rewriteMu       sync.Mutex
}

type Operation struct {
	Type  string // SET or DEL
	Key   string
	Value string
	TTL   int64 // ttl in sec; 0 means it lives forever
}

func AofExists(fileName string) bool {
	_, err := os.Stat(fileName)
	return !os.IsNotExist(err)
}

func NewAOF(fileName string, snapshotName string, policy SyncPolicy, cache *cache.Cache, growthFactor int64) (*AOF, error) {
	// open file for read/write, create if it doesn't exist
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	aof := &AOF{
		file:            file,
		fileName:        fileName,
		snapshotName:    snapshotName,
		policy:          policy,
		cache:           cache,
		done:            make(chan struct{}),
		growthFactor:    growthFactor,
		lastRewriteSize: 0,
		rewriting:       false,
	}

	if policy == SyncEverySecond {
		aof.wg.Add(1)
		go aof.periodicSync()
	}

	// start periodic snapshot goroutine
	aof.wg.Add(1)
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
			slog.Error("Fsync failed", "error", err)
			return fmt.Errorf("fsync failed: %v", err)
		}
	}

	// check ratio trigger
	aof.checkRewriteTrigger()

	return nil
}

// For SyncEverySecond Policy
func (aof *AOF) periodicSync() {
	defer aof.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// sync
			aof.mu.Lock()
			aof.file.Sync()
			aof.mu.Unlock()
		case <-aof.done:
			// got stop signal
			return
		}
	}
}

func (aof *AOF) Close() error {
	select {
	case <-aof.done:
		// already closed
	default:
		close(aof.done)
	}

	aof.wg.Wait()

	// lock during final close to prevent any concurrent operations
	aof.mu.Lock()
	defer aof.mu.Unlock()

	// explicitly sync all pending writes to disk before closing
	if err := aof.file.Sync(); err != nil {
		slog.Warn("Final sync failed", "error", err)
	}

	// close the file handle
	err := aof.file.Close()
	if err != nil {
		return err
	}

	time.Sleep(100 * time.Millisecond)

	return nil
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
			slog.Warn("Skipping corrupted entry", "error", err)
			continue
		}

		// type assert based on what Parse() returns
		partsInterface, ok := result.([]interface{})
		if !ok || len(partsInterface) == 0 {
			continue
		}

		// convert []interface{} to []string
		parts := make([]string, len(partsInterface))
		for i, v := range partsInterface {
			parts[i], ok = v.(string)
			if !ok {
				slog.Warn("Element is not a string", "index", i)
				break
			}
		}

		op := Operation{}
		op.Type = parts[0]
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

func (aof *AOF) rewriteAOF() error {
	// create a new temp aof file
	tempName := aof.fileName + "_temp"
	tempFile, err := os.OpenFile(tempName, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
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
		aofCommand := protocol.EncodeArray([]interface{}{"SET", key, entry.Value, ttlSeconds})

		// write directly to file
		_, err := tempFile.Write([]byte(aofCommand))
		if err != nil {
			slog.Error("Failed to write to temp AOF file", "error", err)
			return fmt.Errorf("failed to write to temp AOF file: %v", err)
		}
	}

	// ensure all writes in tempFile are flushed to disk
	err = tempFile.Sync()
	if err != nil {
		slog.Error("Fsync failed on temp file", "error", err)
		return fmt.Errorf("fsync failed: %v", err)
	}

	err = tempFile.Close()
	tempFile = nil
	if err != nil {
		slog.Error("Closing tempfile failed", "error", err)
		return fmt.Errorf("closing tempfile failed: %v", err)
	}

	// lock during file swap
	aof.mu.Lock()
	defer aof.mu.Unlock()

	// save old file
	oldFile := aof.file

	// close old file
	err = oldFile.Close()
	if err != nil {
		slog.Error("Closing file failed", "error", err)
		return fmt.Errorf("closing file failed: %v", err)
	}

	// rename temp file to original
	err = os.Rename(tempName, aof.fileName)
	if err != nil {
		slog.Error("Renaming file failed", "error", err)
		return fmt.Errorf("renaming file failed: %v", err)
	}

	// reopen file so future writes append to new aof
	newFile, err := os.OpenFile(aof.fileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Error("Failed to reopen new AOF", "error", err)
		return fmt.Errorf("failed to reopen new aof: %v", err)
	}

	aof.file = newFile

	// update baseline size
	info, _ := os.Stat(aof.fileName)
	aof.lastRewriteSize = info.Size()

	// mark rewrite successful
	success = true
	return nil
}

func (aof *AOF) checkRewriteTrigger() {
	aof.rewriteMu.Lock()
	if aof.rewriting {
		aof.rewriteMu.Unlock()
		return
	}
	aof.rewriteMu.Unlock()

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

		aof.wg.Add(1)
		go func() {
			defer aof.wg.Done()

			select {
			case <-aof.done:
				slog.Info("Skipping AOF rewrite due to shutdown")
				aof.rewriteMu.Lock()
				aof.rewriting = false
				aof.rewriteMu.Unlock()
				return
			default:
			}

			err := aof.rewriteAOF()

			aof.mu.Lock()
			aof.rewriting = false
			aof.mu.Unlock()

			if err != nil {
				slog.Error("AOF rewrite failed", "error", err)
			}
		}()
	}
}
