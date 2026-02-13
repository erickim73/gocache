package persistence

import (
	"bufio"
	"fmt"
	"log/slog"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/erickim73/gocache/pkg/protocol"
)

func SnapshotExists(name string) bool {
	_, err := os.Stat(name)
	return !os.IsNotExist(err)
}

func (aof *AOF) CreateSnapshot() error {
	slog.Info("Snapshot creation started",
		"snapshot_file", aof.snapshotName,
	)

	// create a temp snapshot file
	tempName := aof.snapshotName + ".temp.rdb"
	tempFile, err := os.OpenFile(tempName, os.O_CREATE | os.O_WRONLY | os.O_TRUNC, 0644)
	if err != nil {
		slog.Error("Failed to create temp snapshot file",
			"temp_file", tempName,
			"error", err,
		)
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

	itemCount := len(snapshot)
	slog.Debug("Cache snapshot captured",
		"item_count", itemCount,
	)

	writtenCount := 0
	skippedCount := 0

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
				skippedCount++
				continue // skip expired items
			}
		}
		
		ttlSeconds := strconv.Itoa(int(ttl.Seconds()))
		aofCommand := protocol.EncodeArray([]interface{}{"SET", key, entry.Value, ttlSeconds})

		// write directly to file
		_, err := tempFile.Write([]byte(aofCommand))
		if err != nil {
			slog.Error("Failed to write to temp snapshot file",
				"temp_file", tempName,
				"key", key,
				"error", err,
			)
			return fmt.Errorf("failed to write to temp AOF file: %v", err)
		}
		writtenCount++
	}

	slog.Debug("Snapshot data written to temp file",
		"written_items", writtenCount,
		"skipped_expired", skippedCount,
		"temp_file", tempName,
	)

	// ensure all writes in tempFile are flushed to disk
	err = tempFile.Sync()
	if err != nil {
		slog.Error("Failed to fsync temp snapshot file",
			"temp_file", tempName,
			"error", err,
		)
		return fmt.Errorf("fsync failed: %v", err)
	}

	err = tempFile.Close()
	if err != nil {
		slog.Error("Failed to close temp snapshot file",
			"temp_file", tempName,
			"error", err,
		)
		return fmt.Errorf("closing tempfile failed: %v", err)
	}

	// lock during file swap
	aof.mu.Lock()
	defer aof.mu.Unlock()
	
	// rename temp file to original
	err = os.Rename(tempName, aof.snapshotName)
	if err != nil {
		slog.Error("Failed to rename temp snapshot to final",
			"temp_file", tempName,
			"snapshot_file", aof.snapshotName,
			"error", err,
		)
		return fmt.Errorf("renaming file failed: %v", err)
	}

	// close current aof
	aof.file.Close()

	// empty aof
	err = os.Truncate(aof.fileName, 0)
	if err != nil {
		slog.Error("Failed to truncate AOF file",
			"aof_file", aof.fileName,
			"error", err,
		)
		return fmt.Errorf("trncating AOF failed: %v", err)
	}

	// reopen aof file so future append calls work
	newFile, err := os.OpenFile(aof.fileName, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0644)
	if err != nil {
		slog.Error("Failed to reopen AOF file",
			"aof_file", aof.fileName,
			"error", err,
		)
		return fmt.Errorf("failed to reopen new aof: %v", err)
	}
	
	aof.file = newFile

	// mark rewrite successful
	success = true

	slog.Info("Snapshot created successfully",
		"snapshot_file", aof.snapshotName,
		"items_written", writtenCount,
		"items_skipped", skippedCount,
	)

	return nil
}

func (aof *AOF) LoadSnapshot() error {
	slog.Info("Loading snapshot",
		"snapshot_file", aof.snapshotName,
	)

	snapshot, err := os.Open(aof.snapshotName)
	if err != nil {
		slog.Error("Failed to open snapshot file",
			"snapshot_file", aof.snapshotName,
			"error", err,
		)
		return fmt.Errorf("failed to open snapshot: %v", err)
	}
	defer snapshot.Close()

	reader := bufio.NewReader(snapshot)

	// track loading statistics
	loadedCount := 0
	corruptedCount := 0

	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			if err == io.EOF {
				break // reached end of file
			}
			slog.Warn("Skipping corrupted snapshot entry",
				"snapshot_file", aof.snapshotName,
				"error", err,
			)
			corruptedCount++
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
				slog.Warn("Snapshot entry element is not a string",
					"snapshot_file", aof.snapshotName,
					"element_index", i,
					"element_type", fmt.Sprintf("%T", v),
				)
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
			loadedCount++
		}
	}

	slog.Info("Snapshot loaded successfully",
		"snapshot_file", aof.snapshotName,
		"items_loaded", loadedCount,
		"corrupted_entries", corruptedCount,
	)

	return nil
}

func (aof *AOF) checkSnapshotTrigger() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	slog.Debug("Snapshot trigger started", "interval", "5m")

	for {
		select {
		case <- ticker.C:

			slog.Debug("Snapshot trigger fired, creating snapshot")

			// create snapshot
			startTime := time.Now()
			err := aof.CreateSnapshot()
			duration := time.Since(startTime)

			if err != nil {
				slog.Error("Snapshot creation failed",
					"snapshot_file", aof.snapshotName,
					"duration_ms", duration.Milliseconds(),
					"error", err,
				)
			} else {
				slog.Info("Periodic snapshot completed",
					"snapshot_file", aof.snapshotName,
					"duration_ms", duration.Milliseconds(),
				)
			}
		case <- aof.done:
			// log shutdown
			slog.Debug("Snapshot trigger stopping (AOF closed)")

			// stop when aof is closed
			return
		}

	}
}
