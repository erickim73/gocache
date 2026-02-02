package cluster

import (
	"encoding/binary"
	"fmt"
	"sort"
	"sync"

	"crypto/sha256"
)

type HashRing struct {
	hashValues []uint32 // store sorted list of hash values for binary search
	hashToShard map[uint32]string // map hash values to shard IDs
	nodeAddresses map[string]string 
	virtualNodes int // number of virtual nodes per server
	shardToNodes map[string][]string // shard ID -> [leader, follower1, follower2...]
	mu sync.RWMutex
}

// creates a new hash ring with the specified number of virtual nodes per physical node
func NewHashRing(virtualNodes int) *HashRing {
	return &HashRing{
		hashValues: make([]uint32, 0),
		hashToShard: make(map[uint32]string),
		nodeAddresses: make(map[string]string),
		shardToNodes: make(map[string][]string),
		virtualNodes: virtualNodes,
	}
}

// computes a hash value for a given key using sha-256 and taking first 4 bytes as uint32
func (hr *HashRing) hash(key string) uint32 {
	// create sha-256 hash of key
	h := sha256.New()

	// write key bytes to hash function
	h.Write([]byte(key))

	// get computed hash as a byte slice
	hashBytes := h.Sum(nil)

	// convert first 4 bytes to uint32 using big-endian byte order
	// only need 32 bits (4 bytes) to create enough hash space 
	return binary.BigEndian.Uint32(hashBytes[:4])
}

// adds a shard to the hash ring. creates multiple virtual nodes for the shard
func (hr *HashRing) AddShard(shardID string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	for i := 0; i < hr.virtualNodes; i++ {
		// create unique virtual node key and hash it
		virtualKey := fmt.Sprintf("%s-%d", shardID, i)
		hashValue := hr.hash(virtualKey)
		
		// store mapping from hash position and add hash value to sorted list
		hr.hashToShard[hashValue] = shardID
		hr.hashValues = append(hr.hashValues, hashValue)
	}

	// sort hash values
	sort.Slice(hr.hashValues, func(i int, j int) bool {
		return hr.hashValues[i] < hr.hashValues[j]
	})

	fmt.Printf("[HASH RING] Added shard %s with %d virtual nodes\n", shardID, hr.virtualNodes)

}

// removes a physical node and all its virtual nodes from the ring
func (hr *HashRing) RemoveNode(shardID string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	for i := 0; i < hr.virtualNodes; i++ {
		// recreate unique virtual node key and hash it
		virtualKey := fmt.Sprintf("%s-%d", shardID, i)
		hashValue := hr.hash(virtualKey)

		// remove from hash-to-node mapping
		delete(hr.hashToShard, hashValue)

		// remove hash value from sorted slice
		for idx, val := range hr.hashValues {
			if val == hashValue {
				hr.hashValues = append(hr.hashValues[:idx], hr.hashValues[idx + 1:]...)
				break
			}
		}
	}
}

// returns the node responsible for the given key
func (hr *HashRing) GetNode(key string) (string, error) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	// check if ring is empty
	if len(hr.hashValues) == 0 {
		return "", fmt.Errorf("hash ring is empty")
	}

	keyHash := hr.hash(key)

	// binary search to find first virtual node >= keyHash
	idx := sort.Search(len(hr.hashValues), func(i int) bool {
		return hr.hashValues[i] >= keyHash
	})

	// handle wrap around case
	if idx >= len(hr.hashValues) {
		idx = 0
	}

	// get hash value at index
	hashValue := hr.hashValues[idx]

	// look up which physical node owns this virtual node
	nodeID := hr.hashToShard[hashValue]

	return nodeID, nil
}

// returns all nodes in the ring
func (hr *HashRing) GetNodes() []string {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	// set to collect unique physical nodes
	uniqueNodes := make(map[string]bool)

	// iterate through all hash->node mappings
	for _, nodeID := range hr.hashToShard {
		uniqueNodes[nodeID] = true
	}

	// convert set to slice
	nodes := make([]string, 0, len(uniqueNodes))
	for nodeID := range uniqueNodes {
		nodes = append(nodes, nodeID)
	}

	return nodes
}

// returns the number of physical nodes
func (hr *HashRing) GetNodeCount() int {
	return len(hr.GetNodes())
}

// exported version of hash() for external use
func (hr *HashRing) Hash(key string) uint32 {
	return hr.hash(key)
}

// returns the network address for a given node ID
func (hr *HashRing) GetNodeAddress(nodeID string) string {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	if hr.nodeAddresses == nil {
		return ""
	}
	return hr.nodeAddresses[nodeID]
}

// returns all unique node IDs in the ring
func (hr *HashRing) GetAllNodes() []string {
	return hr.GetNodes()
}

// sets node addresses for hash ring
func (hr *HashRing) SetNodeAddress(nodeID string, address string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	
	if hr.nodeAddresses == nil {
		hr.nodeAddresses = make(map[string]string)
	}
	hr.nodeAddresses[nodeID] = address
}

// returns the shard ID responsible for the given key
func (hr *HashRing) GetShard(key string) (string, error) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	// check if ring is empty
	if len(hr.hashValues) == 0 {
		return "", fmt.Errorf("hash ring is empty")
	}

	keyHash := hr.hash(key)

	// binary search to find first virtual node >= keyHash
	idx := sort.Search(len(hr.hashValues), func(i int) bool {
		return hr.hashValues[i] >= keyHash
	})

	// handle wrap around case
	if idx >= len(hr.hashValues) {
		idx = 0
	}

	// get hash value at index
	hashValue := hr.hashValues[idx]

	// look up which shard owns this virtual node
	shardID := hr.hashToShard[hashValue]

	return shardID, nil
}

// sets which nodes belong to a shard [leader, follower1, follower2...]
func (hr *HashRing) SetShardNodes(shardID string, nodeIDs []string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	hr.shardToNodes[shardID] = nodeIDs
	fmt.Printf("[HASH RING] Set shard %s nodes: %v\n", shardID, nodeIDs)
}

// returns all nodes in a shard [leader1, follower1, ...]
func (hr *HashRing) GetShardNodes(shardID string) ([]string, error) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	nodes, exists := hr.shardToNodes[shardID]
	if !exists {
		return nil, fmt.Errorf("shard %s not found", shardID)
	}

	return nodes, nil
}

// returns the leader node ID for a shard (first node in list)
func (hr *HashRing) GetShardLeader(shardID string) (string, error) {
	nodes, err := hr.GetShardNodes(shardID) 
	if err != nil {
		return "", err
	}

	if len(nodes) == 0 {
		return "", fmt.Errorf("shard %s has no nodes", shardID)
	}

	// first node is always the leader
	return nodes[0], nil
}

// returns follower node IDs for a shard
func (hr *HashRing) GetShardFollowers(shardID string) ([]string, error) {
	nodes, err := hr.GetShardNodes(shardID)
	if err != nil {
		return nil, err
	}

	if len(nodes) <= 1 {
		return []string{}, nil // no followers
	}

	// all nodes after first are followers
	return nodes[1:], nil
}

// returns all unique shard IDs in the ring
func (hr *HashRing) GetAllShards() []string {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	uniqueShards := make(map[string]bool)
	for _, shardID := range hr.hashToShard {
		uniqueShards[shardID] = true
	}

	shards := make([]string, 0, len(uniqueShards))
	for shardID := range uniqueShards {
		shards = append(shards, shardID)
	}

	return shards
}