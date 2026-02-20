package cluster

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/erickim73/gocache/pkg/protocol"
)

// represents the health state of a node
type NodeStatus int

const (
	NodeStatusAlive     NodeStatus = iota
	NodeStatusSuspected            // missed some pings but not confirmed dead
	NodeStatusDead                 // confirmed dead after multiple failures
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
	NodeID               string
	Address              string
	Status               NodeStatus
	LastSuccessfulPing   time.Time
	LastFailedPing       time.Time
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
}

// monitors the health of all nodes in the cluster
type HealthChecker struct {
	nodeHealth map[string]*NodeHealth // nodeID -> health info

	// config
	checkInterval    time.Duration // how often to check (5 sec)
	failureThreshold int           // failures before marking dead (3)
	timeout          time.Duration // ping timeout (2 sec)

	// dependencies
	hashRing        *HashRing
	onNodeFailed    func(nodeID string) // callback when node dies
	onNodeRecovered func(nodeID string) // callback when node recovers

	mu     sync.RWMutex
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// creates a new health checker
func NewHealthChecker(hashRing *HashRing, checkInterval time.Duration, failureThreshold int, timeout time.Duration) *HealthChecker {
	return &HealthChecker{
		nodeHealth:       make(map[string]*NodeHealth),
		checkInterval:    checkInterval,
		failureThreshold: failureThreshold,
		timeout:          timeout,
		hashRing:         hashRing,
		stopCh:           make(chan struct{}),
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
	if !exists {
		hc.nodeHealth[nodeID] = &NodeHealth{
			NodeID:               nodeID,
			Address:              address,
			Status:               NodeStatusAlive,
			LastSuccessfulPing:   time.Now(),
			ConsecutiveSuccesses: 1,
		}
		slog.Info("Health checker: node registered",
			"node_id", nodeID,
			"address", address,
		)
	}
}

// removes a node from health checking
func (hc *HealthChecker) UnregisterNode(nodeID string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	delete(hc.nodeHealth, nodeID)
	slog.Info("Health checker: node unregistered", "node_id", nodeID)
}

// begins the health checking loop
func (hc *HealthChecker) Start() {
	hc.wg.Add(1)
	go hc.healthCheckLoop()

	slog.Info("Health checker started",
		"check_interval", hc.checkInterval,
		"failure_threshold", hc.failureThreshold,
		"timeout", hc.timeout,
		"monitored_nodes", len(hc.nodeHealth),
	)
}

// stops the health checker
func (hc *HealthChecker) Stop() {
	close(hc.stopCh)
	hc.wg.Wait()

	slog.Info("Health checker stopped")
}

// runs periodic health checks
func (hc *HealthChecker) healthCheckLoop() {
	defer hc.wg.Done()

	ticker := time.NewTicker(hc.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hc.checkAllNodes()
		case <-hc.stopCh:
			return
		}
	}
}

// checks the health of all registered nodes
func (hc *HealthChecker) checkAllNodes() {
	hc.mu.RLock()
	nodes := make([]*NodeHealth, 0, len(hc.nodeHealth))
	for _, node := range hc.nodeHealth {
		nodes = append(nodes, node)
	}
	hc.mu.RUnlock()

	for _, node := range nodes {
		hc.checkNode(node)
	}
}

// pings a single node and updates its health status
func (hc *HealthChecker) checkNode(node *NodeHealth) {
	err := hc.pingNode(node.Address)

	hc.mu.Lock()
	defer hc.mu.Unlock()

	if err != nil {
		// ping failed
		node.ConsecutiveFailures++
		node.ConsecutiveSuccesses = 0
		node.LastFailedPing = time.Now()

		slog.Warn("Health check failed",
			"node_id", node.NodeID,
			"address", node.Address,
			"error", err,
			"consecutive_failures", node.ConsecutiveFailures,
			"threshold", hc.failureThreshold,
		)

		// update status based on failure count
		previousStatus := node.Status

		if node.ConsecutiveFailures >= hc.failureThreshold {
			node.Status = NodeStatusDead
		} else if node.ConsecutiveFailures > 0 {
			node.Status = NodeStatusSuspected
		}

		// trigger callback on state transition to dead
		if previousStatus != NodeStatusDead && node.Status == NodeStatusDead {
			slog.Error("Node marked as DEAD",
				"node_id", node.NodeID,
				"address", node.Address,
				"consecutive_failures", node.ConsecutiveFailures,
			)

			if hc.onNodeFailed != nil {
				// call callback outside of lock to avoid deadlock
				go hc.onNodeFailed(node.NodeID)
			}
		}
	} else {
		// ping succeeded
		node.ConsecutiveSuccesses++
		node.ConsecutiveFailures = 0
		node.LastSuccessfulPing = time.Now()

		previousStatus := node.Status
		node.Status = NodeStatusAlive

		// trigger callback on recovery
		if previousStatus == NodeStatusDead {
			slog.Info("Node RECOVERED",
				"node_id", node.NodeID,
				"address", node.Address,
				"was_down_for", time.Since(node.LastFailedPing),
			)

			if hc.onNodeRecovered != nil {
				go hc.onNodeRecovered(node.NodeID)
			}
		} else if previousStatus == NodeStatusSuspected {
			slog.Debug("Node health restored",
				"node_id", node.NodeID,
				"address", node.Address,
			)
		}
	}
}

// sends a PING command to a node and expects PONG
func (hc *HealthChecker) pingNode(address string) error {
	//connect with timeout
	conn, err := net.DialTimeout("tcp", address, hc.timeout)
	if err != nil {
		return fmt.Errorf("connection failed: %v", err)
	}
	defer conn.Close()

	// set deadline for entire operation
	conn.SetDeadline(time.Now().Add(hc.timeout))

	reader := bufio.NewReader(conn)

	// send PING command
	cmd := protocol.EncodeArray([]interface{}{"PING"})
	_, err = conn.Write([]byte(cmd))
	if err != nil {
		return fmt.Errorf("write failed: %v", err)
	}

	// read response
	response, err := protocol.Parse(reader)
	if err != nil {
		return fmt.Errorf("read failed: %v", err)
	}

	// check for pong
	str, ok := response.(string)
	if ok {
		if str == "PONG" {
			return nil
		}
		return fmt.Errorf("unexpected response: %s", str)
	}

	return fmt.Errorf("invalid response type: %T", response)
}

// returns true if the node is alive
func (hc *HealthChecker) IsNodeHealthy(nodeID string) bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	health, exists := hc.nodeHealth[nodeID]
	if !exists {
		return false
	}

	return health.Status == NodeStatusAlive
}

// returns a list of all healthy node IDs
func (hc *HealthChecker) GetHealthyNodes() []string {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	healthy := []string{}
	for nodeID, health := range hc.nodeHealth {
		if health.Status == NodeStatusAlive {
			healthy = append(healthy, nodeID)
		}
	}

	return healthy
}

// returns the current status of a node
func (hc *HealthChecker) GetNodeStatus(nodeID string) NodeStatus {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	health, exists := hc.nodeHealth[nodeID]
	if !exists {
		return NodeStatusDead
	}

	return health.Status
}

// returns a copy of all node health information
func (hc *HealthChecker) GetAllNodeStatus() map[string]*NodeHealth {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	statusCopy := make(map[string]*NodeHealth)
	for nodeID, health := range hc.nodeHealth {
		// create a copy
		healthCopy := *health
		statusCopy[nodeID] = &healthCopy
	}

	return statusCopy
}
