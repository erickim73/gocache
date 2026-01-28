package cluster

import (
	"sync"
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