package server

import (
	"fmt"
	"log/slog"
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
func (ns *NodeState) ShouldForwardRequest(key string, isWrite bool) (bool, string, string) {
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

	slog.Debug("Key routing decision", "key", key, "responsible_shard", responsibleShardID)

	// check if this key belongs to my shard
	myShard, err := ns.config.GetShardForNode(ns.config.NodeID)
	if err != nil {
		return false, "", ""
	}

	// if key belongs to my shard, i can handle it
	if responsibleShardID == myShard.ShardID {
		// key belongs to my shard

		// for reads, followers can serve locally
		if !isWrite {
			slog.Debug("Read request in my shard - handling locally", "key", key, "shard_id", myShard.ShardID)
			return false, "", "" // handle locally (even if follower)
		}

		// for writes (SET/DEL), must go to shard leader
		leaderNodeID, err := ns.hashRing.GetShardLeader(responsibleShardID)
		if err != nil {
			return false, "", ""
		}

		if leaderNodeID == ns.config.NodeID {
			slog.Debug("Write request - I am shard leader, handling locally", "key", key)
			return false, "", "" // i'm leader, handle locally
		}

		// follower, forward write to leader
		var targetAddr string
		for _, node := range ns.config.Nodes {
			if node.ID == leaderNodeID {
				targetAddr = fmt.Sprintf("%s:%d", node.Host, node.Port)
				break
			}
		}

		slog.Debug("Write request - forwarding to shard leader", "key", key, "leader_node_id", leaderNodeID)
		return true, leaderNodeID, targetAddr
	}


	// key belongs to different shard, find that shard's leader 
	leaderNodeID, err := ns.hashRing.GetShardLeader(responsibleShardID)
	if err != nil {
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

	slog.Debug("Key belongs to different shard, forwarding to leader", "key", key, "shard_id", responsibleShardID, "leader_node_id", leaderNodeID)
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