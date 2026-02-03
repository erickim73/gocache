package server

import (
	"fmt"
	"sync"

	"github.com/erickim73/gocache/internal/cluster"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/internal/cache"
)

// NodeState holds mutable state that can change during runtime
type NodeState struct {
	role       string
	leader     *replication.Leader  // only set when this node is the leader
	leaderAddr string 
	mu         sync.RWMutex

	// cluster routing components for key distribution
	hashRing *cluster.HashRing // determines which nodes owns which keys
	config *config.Config // cluster configuration with all node info
	migrator *cluster.Migrator
	cache *cache.Cache
	healthChecker *cluster.HealthChecker
}

// check if running in cluster mode
func (ns *NodeState) IsClusterMode() bool {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.hashRing != nil && ns.config != nil && ns.config.IsClusterMode()
}

// determine if this node should handle a key. returns (shouldForward, targetNodeID, targetAddress)
func (ns *NodeState) ShouldForwardRequest(key string) (bool, string, string) {
	ns.mu.RLock()
	defer ns.mu.RUnlock()

	// if not in cluster mode, always handle locally
	if !ns.IsClusterMode() {
		return false, "", ""
	}

	// ask hash ring which shard owns this key
	responsibleShardID, err := ns.hashRing.GetShard(key)
	if err != nil {
		// handle locally if hash ring fails
		return false, "", ""
	}

	fmt.Printf("[DEBUG] key '%s' → hash ring says: shard '%s'\n", key, responsibleShardID)

	// find leader of that shard
	leaderNodeID, err := ns.hashRing.GetShardLeader(responsibleShardID)
	if err != nil {
		return false, "", ""
	}

	fmt.Printf("[DEBUG] Shard '%s' leader: '%s', my ID: '%s'\n", responsibleShardID, leaderNodeID, ns.config.NodeID)
	
	// check if we are the leader
	if leaderNodeID == ns.config.NodeID {
		return false, "", ""
	}

	// find leader node's address from cluster config
	var targetAddr string
	for _, node := range ns.config.Nodes {
		if node.ID == leaderNodeID {
			targetAddr = fmt.Sprintf("%s:%d", node.Host, node.Port)
			break
		}
	}

	fmt.Printf("[DEBUG] Forwarding key '%s' to shard leader '%s' at '%s'\n", key, leaderNodeID, targetAddr)

	if targetAddr == "" {
		// couldn't find node address, handle locally
		return false, "", ""
	}

	// forward to another node
	return true, leaderNodeID, targetAddr
}

// set hash ring for cluster routing
func (ns *NodeState) SetHashRing(hr *cluster.HashRing) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	
	ns.hashRing = hr
}

// set config for cluster information
func (ns *NodeState) SetConfig(cfg *config.Config) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	
	ns.config = cfg
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

func NewNodeState(role string, leader *replication.Leader, leaderAddr string) (*NodeState, error) {
	return &NodeState{
		role: role,
		leader: leader,
		leaderAddr: leaderAddr,
		hashRing: nil,
		config: nil,
	}, nil
}

// returns the hash ring for cluster operations
func (ns *NodeState) GetHashRing() *cluster.HashRing {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.hashRing
}

// returns the config for cluster operations
func (ns *NodeState) GetConfig() *config.Config {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.config
}

// returns the migrator for data migration operations
func (ns *NodeState) GetMigrator() *cluster.Migrator {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.migrator
}

// sets the migrator for data migration operations
func (ns *NodeState) SetMigrator(migrator *cluster.Migrator) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.migrator = migrator
}

// sets the cache reference
func (ns *NodeState) SetCache(c *cache.Cache) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.cache = c
}

// gets HealthChecker
func (ns *NodeState) GetHealthChecker() *cluster.HealthChecker {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.healthChecker
}

// sets HealthChecker
func (ns *NodeState) SetHealthChecker(hc *cluster.HealthChecker) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.healthChecker = hc
}