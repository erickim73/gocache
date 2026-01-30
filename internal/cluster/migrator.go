package cluster

import (
	"fmt"
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

	fmt.Printf("[MIGRATION] Starting migration to %s at %s\n", newNodeID, newNodeAddr)

	// calculate which keys need to move by determining which has range will be owned by new node
	tasks := m.hashRing.CalculateMigrations(newNodeID)

	if len(tasks) == 0 {
		fmt.Printf("[MIGRATION] No keys to migrate\n")
		// add node to ring even if no data to migrate
		m.hashRing.AddNode(newNodeID)
		return nil
	}

	fmt.Printf("[MIGRATION] Found %d migration tasks\n", len(tasks))

	// track total keys migrated for logging
	totalKeysMigrated := 0

	// execute each migration task
	for i, task := range tasks {
		fmt.Printf("[MIGRATION] Task %d/%d: %s -> %s (has range [%d, %d))])\n", i + 1, len(tasks), task.FromNode, task.ToNode, task.StartHash, task.EndHash)

		// get keys that fall within this hash range
		keys := m.cache.GetKeysInHashRange(task.StartHash, task.EndHash, m.hashRing.Hash)

		if len(keys) == 0 {
			fmt.Printf("[MIGRATION] Task %d: No keys in this range\n", i + 1)
			continue
		}

		fmt.Printf("[MIGRATION] Task %d: Found %d keys to migrate\n", i + 1, len(keys))

		// get values for those keys
		values := make(map[string]string)
		for _, key := range keys {
			value, exists := m.cache.Get(key)
			if exists {
				values[key] = value
			}
		}

		// transfer keys to target node over network
		fmt.Printf("[MIGRATION] Task %d: Transferring keys to %s\n", i + 1, newNodeAddr)
		err := TransferKeysBatch(newNodeAddr, keys, values, 100) // 100 keys per batch
		if err != nil {
			// if transfer fails, abort entire migration
			return fmt.Errorf("task %d transfer failed: %v", i + 1, err)
		}

		// verify transfer before deleting
		fmt.Printf("[MIGRATION] Task %d: Verifying transfer...\n", i + 1)
		err = VerifyTransfer(newNodeAddr, keys)
		if err != nil {
			return fmt.Errorf("task %d verification failed: %v", i + 1, err)
		}

		// delete from local cache only after successful transfer
		fmt.Printf("[MIGRATION] Task %d: Deleting keys from local cache\n", i + 1)
		for _, key := range keys {
			m.cache.Delete(key)
		}

		totalKeysMigrated += len(keys)
		fmt.Printf("[MIGRATION] Task %d complete: %d keys migrated\n", i + 1, len(keys))
	}

	// add node to hash ring
	fmt.Printf("[MIGRATION] Adding %s to hash ring\n", newNodeID)
	m.hashRing.AddNode(newNodeID)

	fmt.Printf("[MIGRATION] Migration complete: %d total keys migrated to %s\n", totalKeysMigrated, newNodeID)

	return nil
}