package cluster

import (
	"fmt"
	"sync"
	"time"
)

// represents the health state of a node
type NodeStatus int

const (
	NodeStatusAlive NodeStatus = iota
	NodeStatusSuspected // missed some pings but not confirmed dead
	NodeStatusDead // confirmed dead after multiple failures
)

func (ns NodeStatus) String() string {
	switch ns {
	case NodeStatusAlive:
		return "alive"
	case NodeStatusSuspected:
		return "suspected"
	case NodeStatusDead:
		return "dead"
	default:
		return "unknown"
	}
}

// tracks the health information for a single node
type NodeHealth struct {
	NodeID string
	Address string
	Status NodeStatus
	LastSuccessfulPing time.Time
	LastFailedPing time.Time
	ConsecutiveFailures int
	ConsecutiveSuccesses int
}

// monitors the health of all nodes in the cluster
type HealthChecker struct {
	nodeHealth map[string]*NodeHealth // nodeID -> health info

	// config
	checkInterval time.Duration // how often to check (5 sec)
	failureThreshold int // failures before marking dead (3)
	timeout time.Duration // ping timeout (2 sec)

	// dependencies
	hashRing *HashRing
	onNodeFailed func(nodeID string) // callback when node dies
	onNodeRecovered func(nodeID string) // callback when node recovers

	mu sync.RWMutex
	stopCh chan struct{}
	wg sync.WaitGroup
}

// creates a new health checker
func NewHealthChecker(hashRing *HashRing, checkInterval time.Duration, failureThreshold int, timeout time.Duration) *HealthChecker {
	return &HealthChecker{
		nodeHealth: make(map[string] *NodeHealth),
		checkInterval: checkInterval,
		failureThreshold: failureThreshold,
		timeout: timeout,
		hashRing: hashRing,
		stopCh: make(chan struct{}),
	}
}

// sets the callbacks for node failure/recovery events
func (hc *HealthChecker) SetCallbacks(onFailed func(string), onRecovered func(string)) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.onNodeFailed = onFailed
	hc.onNodeRecovered = onRecovered
}

// adds a node to the health checker
func (hc *HealthChecker) RegisterNode(nodeID string, address string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	_, exists := hc.nodeHealth[nodeID]
	if ! exists {
		hc.nodeHealth[nodeID] = &NodeHealth{
			NodeID: nodeID,
			Address: address,
			Status: NodeStatusAlive,
			LastSuccessfulPing: time.Now(),
			ConsecutiveSuccesses: 1,
		}
		fmt.Printf("[HEALTH] Registered node %s at %s\n", nodeID, address)
	}
}