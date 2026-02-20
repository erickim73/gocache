package persistence

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/erickim73/gocache/internal/cache"
)

// reads aof and applies operations to cache
func RecoverAOF(cache *cache.Cache, aof *AOF, aofName string, snapshotName string) error {
	slog.Info("Starting AOF recovery",
		"aof_file", aofName,
		"snapshot_file", snapshotName,
	)

	// check for snapshot first
	if SnapshotExists(snapshotName) {
		slog.Info("Snapshot file found, loading...", "file", snapshotName)

		err := aof.LoadSnapshot()
		if err != nil {
			slog.Error("Failed to load snapshot",
				"file", snapshotName,
				"error", err,
			)
			return fmt.Errorf("error loading snapshot: %v", err)
		}
		slog.Info("Snapshot loaded successfully",
			"file", snapshotName,
		)
	} else {
		slog.Info("No snapshot file found, will start from AOF or empty", "file", snapshotName)
	}

	// check for aof file
	if AofExists(aofName) {
		slog.Info("AOF file found, reading operations...", "file", aofName)

		ops, err := aof.ReadOperations()
		if err != nil {
			slog.Error("Failed to read AOF operations",
				"file", aofName,
				"error", err,
			)
			return fmt.Errorf("error reading operations from aof: %v", err)
		}

		slog.Info("AOF operations read",
			"file", aofName,
			"operation_count", len(ops),
		)

		// track recovery progress
		recoveredCount := 0
		startTime := time.Now()

		for i, op := range ops {
			if op.Type == "SET" {
				ttl := time.Duration(op.TTL) * time.Second
				err := cache.Set(op.Key, op.Value, ttl)
				if err != nil {
					slog.Error("Failed to apply SET operation",
						"operation_index", i,
						"key", op.Key,
						"error", err,
					)
					return fmt.Errorf("error applying set operation on cache")
				}
				recoveredCount++

			} else if op.Type == "DEL" {
				err := cache.Delete(op.Key)
				if err != nil {
					slog.Error("Failed to apply DELETE operation",
						"operation_index", i,
						"key", op.Key,
						"error", err,
					)
					return fmt.Errorf("error deleting key from cache")
				}
				recoveredCount++
			}

			// log progress for large recoveries (every 1000 operations)
			if (i+1)%1000 == 0 {
				slog.Info("Recovery progress",
					"recovered", i+1,
					"total", len(ops),
					"percent", fmt.Sprintf("%.1f%%", float64(i+1)/float64(len(ops))*100),
				)
			}
		}

		// calculate recovery time
		duration := time.Since(startTime)

		slog.Info("AOF recovery completed successfully",
			"file", aofName,
			"operations_recovered", recoveredCount,
			"duration_ms", duration.Milliseconds(),
			"ops_per_second", fmt.Sprintf("%.0f", float64(recoveredCount)/duration.Seconds()),
		)
	} else {
		slog.Info("No AOF file found, starting with empty cache", "file", aofName)
	}

	return nil
}
