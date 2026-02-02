package cluster

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/erickim73/gocache/pkg/protocol"
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

// removes a node from health checking
func (hc *HealthChecker) UnregisterNode(nodeID string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	delete(hc.nodeHealth, nodeID)
	fmt.Printf("[HEALTH] Unregistered node %s\n", nodeID)
}

// begins the health checking loop
func (hc *HealthChecker) Start() {
	hc.wg.Add(1)
	go hc.healthCheckLoop()
	fmt.Printf("[HEALTH] Health checker started (interval: %v, threshold: %d, timeout: %v)\n", hc.checkInterval, hc.failureThreshold, hc.timeout)
}

// stops the health checker
func (hc *HealthChecker) Stop() {
	close(hc.stopCh)
	hc.wg.Wait()
	fmt.Printf("[HEALTH] Health checker stopped\n")
}

// runs periodic health checks
func (hc *HealthChecker) healthCheckLoop() {
	defer hc.wg.Done()

	ticker := time.NewTicker(hc.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <- ticker.C:
			hc.checkAllNodes()
		case <- hc.stopCh: 
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

		fmt.Printf("[HEALTH] Ping failed for node %s (%s): %v (failures: %d/%d)\n", node.NodeID, node.Address, err, node.ConsecutiveFailures, hc.failureThreshold)

		// update status based on failure count
		previousStatus := node.Status

		if node.ConsecutiveFailures >= hc.failureThreshold {
			node.Status = NodeStatusDead
		} else if node.ConsecutiveFailures > 0 {
			node.Status = NodeStatusSuspected
		}

		// trigger callback on state transition to dead
		if previousStatus != NodeStatusDead && node.Status == NodeStatusDead {
			fmt.Printf("[HEALTH] Node %s marked as DEAD after %d failures\n", node.NodeID, node.ConsecutiveFailures)
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
			fmt.Printf("[HEALTH] Node %s RECOVERED\n", node.NodeID)
			if hc.onNodeRecovered != nil {
				go hc.onNodeRecovered(node.NodeID)
			}
		} else if previousStatus == NodeStatusSuspected {
			fmt.Printf("[HEALTH] Node %s back to healthy\n", node.NodeID)
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