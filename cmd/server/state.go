package main

import (
	"sync"

	"github.com/erickim73/gocache/internal/replication"
)

// NodeState holds mutable state that can change during runtime
type NodeState struct {
	role       string
	leader     *replication.Leader  // only set when this node is the leader
	leaderAddr string 
	mu         sync.RWMutex
}

func (ns *NodeState) GetRole() string {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.role
}

func (ns *NodeState) SetRole(role string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.role = role
}

func (ns *NodeState) GetLeader() *replication.Leader {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.leader
}

func (ns *NodeState) SetLeader(leader *replication.Leader) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.leader = leader
}

func (ns *NodeState) GetLeaderAddr() string {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.leaderAddr
}

func (ns *NodeState) SetLeaderAddr(addr string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.leaderAddr = addr
}