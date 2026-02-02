package cluster

import (
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