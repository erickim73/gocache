package server

import (
	"fmt"
	"sync"

	"github.com/erickim73/gocache/internal/cluster"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/replication"
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

	// ask hash ring which node owns this key
	responsibleNodeID, err := ns.hashRing.GetNode(key)
	if err != nil {
		// hanlde locally if hash ring fails
		return false, "", ""
	}

	// check if we're the responsible node
	if responsibleNodeID == ns.config.NodeID {
		// node owns this key, handle locally
		return false, "", ""
	}

	// find target node's address from cluster config
	var targetAddr string
	for _, node := range ns.config.Nodes {
		if node.ID == responsibleNodeID {
			targetAddr = fmt.Sprintf("%s:%d", node.Host, node.Port)
			break
		}
	}

	if targetAddr == "" {
		// couldn't find node address, handle locally
		return false, "", ""
	}

	// forward to another node
	return true, responsibleNodeID, targetAddr
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
	}, nil
}