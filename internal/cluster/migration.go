package cluster

import (
	"fmt"
	"sort"
)

// represents keys that need to move
type MigrationTask struct {
	FromNode string
	ToNode string
	KeyPattern string // for now, migrate all keys (pattern "*")
	StartHash uint32 // hash range start 
	EndHash uint32 // hash range end
}

// determines which keys need to move when a new node joins
func (hr *HashRing) CalculateMigrations(newNodeID string) []MigrationTask {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	tasks := []MigrationTask{}

	// for each virtual node we're adding, we need to calculate migrations
	for i := 0; i < hr.virtualNodes; i++ {
		vnodeName := fmt.Sprintf("%s-%d", newNodeID, i)
		vhash := hr.hash(vnodeName)

		// find where this virtual node will be inserted in the sorted ring
		idx := sort.Search(len(hr.hashValues), func(i int) bool {
			return hr.hashValues[i] >= vhash
		})

		// if hash is larger than all existing values, it wraps around to beginning of ring
		if idx == len(hr.hashValues) {
			idx = 0
		}

		// find previous node on the ring
		var prevHash uint32
		var prevNode string

		if idx == 0 {
			// previous node is at the end of the ring
			prevHash = hr.hashValues[len(hr.hashValues) - 1]
		} else { 
			// previous node is just before insertion point
			prevHash = hr.hashValues[idx - 1]
		}

		// look up which physical node owns this hash value
		prevNode = hr.hashToNode[prevHash]

		// create a migration task if we're taking from a different node
		if prevNode != newNodeID {
			task := MigrationTask {
				FromNode: prevNode,
				ToNode: newNodeID,
				KeyPattern: "*", // migrate all keys in this range
				StartHash: prevHash,
				EndHash: vhash,
			}
			tasks = append(tasks, task)

			fmt.Printf("[MIGRATION] Task created: %s [%d->%d] -> %s\n", prevNode, prevHash, vhash, newNodeID)
		}
	}
	fmt.Printf("[MIGRATION] Calculated %d migration tasks for %s\n", len(tasks), newNodeID)
		
	return tasks
}

// determines where keys should go when a node leaves
func (hr *HashRing) CalculateMigrationsForRemoval(removeNodeID string) []MigrationTask {
	hr.mu.RLock()
	defer hr.mu.Unlock()

	tasks := []MigrationTask{}

	// find all virtual nodes belonging to the node being removed
	for i := 0; i < hr.virtualNodes; i++ {
		vnodeName := fmt.Sprintf("%s-%d", removeNodeID, i)
		vhash := hr.hash(vnodeName)

		// find this virtual node's position in the ring
		idx := sort.Search(len(hr.hashValues), func(i int) bool {
			return hr.hashValues[i] >= vhash
		})

		// find the next node on the ring
		var nextHash uint32
		var nextNode string

		// the next node is the one immediately after this one
		nextIdx := (idx + 1) % len(hr.hashValues)
		nextHash = hr.hashValues[nextIdx]
		nextNode = hr.hashToNode[nextHash]

		// create task if next node is different from current node
		if nextNode != removeNodeID {
			// previous hash is where the range starts
			var prevHash uint32
			if idx == 0 {
				prevHash = hr.hashValues[len(hr.hashValues) - 1]
			} else {
				prevHash = hr.hashValues[idx - 1]
			}

			task := MigrationTask{
				FromNode: removeNodeID, 
				ToNode: nextNode,
				KeyPattern: "*",
				StartHash: prevHash,
				EndHash: vhash,
			}
			tasks = append(tasks, task)
			
			fmt.Printf("[MIGRATION] Removal tasks: %s [%d->%d] -> %s\n", removeNodeID, prevHash, vhash, nextNode)
		}
	}

	fmt.Printf("[MIGRATION] Calculated %d removal tasks for %s\n", len(tasks), removeNodeID)

	return tasks
} 

