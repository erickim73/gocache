package cluster

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/erickim73/gocache/internal/cache"
)

// orchestrates data migration when nodes join or leave the cluster
// coordinates between the cache, hash ring, and network transfer
type Migrator struct {
	cache *cache.Cache // local cache containing data
	hashRing *HashRing // hash ring for routing decisions
	mu sync.Mutex // prevents concurrent migrations
}

// creates a new migration coordinator
func NewMigrator(cache *cache.Cache, hashRing *HashRing) *Migrator {
	return &Migrator{
		cache: cache,
		hashRing: hashRing,
	}
}

// performs the complete migration when a new node joins
func (m *Migrator) MigrateToNewNode(newNodeID string, newNodeAddr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	slog.Info("Migration started",
		"operation", "add_node",
		"node_id", newNodeID,
		"address", newNodeAddr,
	)

	// calculate which keys need to move by determining which has range will be owned by new node
	slog.Debug("Calculating migration tasks", "node_id", newNodeID)
	
	tasks := m.hashRing.CalculateMigrations(newNodeID)

	// add node to hash ring
	m.hashRing.AddShard(newNodeID)
	m.hashRing.SetNodeAddress(newNodeID, newNodeAddr)

	if len(tasks) == 0 {
		slog.Info("No migration tasks required",
			"node_id", newNodeID,
			"reason", "no keys to migrate",
		)
		return nil
	}

	slog.Info("Migration tasks calculated",
		"node_id", newNodeID,
		"task_count", len(tasks),
	)

	// track total keys migrated for logging
	totalKeysMigrated := 0

	// execute each migration task
	for i, task := range tasks {
		slog.Info("Executing migration task",
			"task_number", i+1,
			"total_tasks", len(tasks),
			"from_node", task.FromNode,
			"to_node", task.ToNode,
			"start_hash", task.StartHash,
			"end_hash", task.EndHash,
		)

		// get keys that fall within this hash range
		keys := m.cache.GetKeysInHashRange(task.StartHash, task.EndHash, m.hashRing.Hash)

		if len(keys) == 0 {
			slog.Debug("No keys in hash range",
				"task_number", i+1,
				"start_hash", task.StartHash,
				"end_hash", task.EndHash,
			)
			continue
		}

		slog.Info("Keys identified for migration",
			"task_number", i+1,
			"key_count", len(keys),
		)

		// get values for those keys
		values := make(map[string]string)
		for _, key := range keys {
			value, exists := m.cache.Get(key)
			if exists {
				values[key] = value
			}
		}

		// transfer keys to target node over network
		slog.Info("Transferring keys to target",
			"task_number", i+1,
			"target_address", newNodeAddr,
			"key_count", len(keys),
		)

		err := TransferKeysBatch(newNodeAddr, keys, values, 100) // 100 keys per batch
		if err != nil {
			// if transfer fails, abort entire migration
			return fmt.Errorf("task %d transfer failed: %v", i + 1, err)
		}

		// verify transfer before deleting
		slog.Debug("Verifying transfer", "task_number", i+1)

		err = VerifyTransfer(newNodeAddr, keys)
		if err != nil {
			return fmt.Errorf("task %d verification failed: %v", i + 1, err)
		}

		// delete from local cache only after successful transfer
		slog.Debug("Deleting migrated keys from local cache",
			"task_number", i+1,
			"key_count", len(keys),
		)
		for _, key := range keys {
			m.cache.Delete(key)
		}

		totalKeysMigrated += len(keys)
		slog.Info("Migration task completed",
			"task_number", i+1,
			"keys_migrated", len(keys),
		)
	}

	slog.Info("Migration completed successfully",
		"operation", "add_node",
		"node_id", newNodeID,
		"total_keys_migrated", totalKeysMigrated,
		"tasks_completed", len(tasks),
	)

	return nil
}

// handles migration when a node leaves
func (m *Migrator) MigrateFromLeavingNode(leavingNodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	slog.Info("Migration started",
		"operation", "remove_node",
		"node_id", leavingNodeID,
	)

	// calculate where keys should move
	tasks := m.hashRing.CalculateMigrationsForRemoval(leavingNodeID)

	if len(tasks) == 0 {
		slog.Info("No migration tasks required",
			"node_id", leavingNodeID,
			"reason", "no keys to migrate",
		)
		m.hashRing.RemoveShard(leavingNodeID)
		m.hashRing.SetNodeAddress(leavingNodeID, "")
		return nil
	}

	slog.Info("Migration tasks calculated for removal",
		"node_id", leavingNodeID,
		"task_count", len(tasks),
	)

	totalKeysMigrated := 0

	// execute each migration task
	for i, task := range tasks {
		slog.Info("Executing removal migration task",
			"task_number", i+1,
			"total_tasks", len(tasks),
			"to_node", task.ToNode,
		)

		// get keys in this range
		keys := m.cache.GetKeysInHashRange(task.StartHash, task.EndHash, m.hashRing.Hash)

		if len(keys) == 0 {
			continue
		}

		// get values
		values := make(map[string]string)
		for _, key := range keys {
			value, exists := m.cache.Get(key)
			if exists {
				values[key] = value
			}
		}

		// get target address
		targetAddr := m.hashRing.GetNodeAddress(task.ToNode)

		// transfer keys
		slog.Info("Transferring keys to successor",
			"task_number", i+1,
			"target_node", task.ToNode,
			"target_address", targetAddr,
			"key_count", len(keys),
		)
		err := TransferKeysBatch(targetAddr, keys, values, 100)
		if err != nil {
			return fmt.Errorf("task %d transfer failed: %v", i + 1, err)
		}

		// verify transfer
		err = VerifyTransfer(targetAddr, keys)
		if err != nil {
			return fmt.Errorf("task %d verification failed: %v", i + 1, err)
		}

		// delete from local cache
		for _, key := range keys {
			m.cache.Delete(key)
		}

		totalKeysMigrated += len(keys)
	}

	// remove node from hash ring
	slog.Info("Removing node from hash ring", "node_id", leavingNodeID)
	m.hashRing.RemoveShard(leavingNodeID)

	slog.Info("Removal migration completed successfully",
		"operation", "remove_node",
		"node_id", leavingNodeID,
		"total_keys_migrated", totalKeysMigrated,
		"tasks_completed", len(tasks),
	)

	return nil
}

// returns information about ongoing migrations
type MigrationStatus struct {
	InProgress bool 
	NodeID string
	Phase string
}

// returns current migration status
func (m *Migrator) Status() MigrationStatus {
	// check if migration lock is held. if we can't acquire it, migration is in progress
	locked := m.mu.TryLock()
	if locked {
		m.mu.Unlock()
		return MigrationStatus{
			InProgress: false,
		}
	}

	return MigrationStatus{
		InProgress: true,
		Phase: "unknown",
	}
}