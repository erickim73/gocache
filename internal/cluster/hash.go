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
	hashToNode map[uint32]string // map hash values to physical server IDs
	virtualNodes int // number of virtual nodes per server
	mu sync.RWMutex
}

// creates a new hash ring with the specified number of virtual nodes per physical node
func NewHashRing(virtualNodes int) *HashRing {
	return &HashRing{
		hashValues: make([]uint32, 0),
		hashToNode: make(map[uint32]string),
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

// adds a physical node to the hash ring. creates multiple virtual nodes for the physical node
func (hr *HashRing) AddNode(nodeID string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	for i := 0; i < hr.virtualNodes; i++ {
		// create unique virtual node key and hash it
		virtualKey := fmt.Sprintf("%s-%d", nodeID, i)
		hashValue := hr.hash(virtualKey)
		
		// store mapping from hash position and add hash value to sorted list
		hr.hashToNode[hashValue] = nodeID
		hr.hashValues = append(hr.hashValues, hashValue)
	}

	// sort hash values
	sort.Slice(hr.hashValues, func(i int, j int) bool {
		return hr.hashValues[i] < hr.hashValues[j]
	})
}