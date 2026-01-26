package main

import (
	"fmt"
	"time"
	
	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"

)

// reads aof and applies operations to cache
func recoverAOF(cache *cache.Cache, aof *persistence.AOF, aofName string, snapshotName string) error {
	if persistence.SnapshotExists(snapshotName) {
		err := aof.LoadSnapshot()
		if err != nil {
			return fmt.Errorf("error loading snapshot: %v", err)
		}
		fmt.Println("Loaded snapshot successfully")
	}

	if persistence.AofExists(aofName) {
		ops, err := aof.ReadOperations()
		if err != nil {
			return fmt.Errorf("error reading operations from aov: %v", err)
		}
		fmt.Printf("Found %d operations from aof to recover\n", len(ops))

		for _, op := range ops {
			if op.Type == "SET" {
				ttl := time.Duration(op.TTL) * time.Second
				err := cache.Set(op.Key, op.Value, ttl)
				if err != nil {
					return fmt.Errorf("error applying set operation on cache")
				}
			} else if op.Type == "DEL" {
				err := cache.Delete(op.Key)
				if err != nil {
					return fmt.Errorf("error deleting key from cache")
				}
			}
		}
		fmt.Printf("Successfully recovered %d operations from aof\n", len(ops))
	}

	return nil
}