package main

import (
	"fmt"
	"net"

	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/google/uuid"
)

func startSimpleMode(cfg *config.Config) {
	// print values to verify
	fmt.Printf("Starting server with config:\n")
	fmt.Printf("Port: %d\n", cfg.Port)
	fmt.Printf("MaxCacheSize: %d\n", cfg.MaxCacheSize)
	fmt.Printf("AOFFileName: %s\n", cfg.AOFFileName)
	fmt.Printf("SnapshotFileName: %s\n", cfg.SnapshotFileName)
	fmt.Printf("SyncPolicy: %s\n", cfg.SyncPolicy)
	fmt.Printf("SnapshotInterval: %v\n", cfg.SnapshotInterval)
	fmt.Printf("GrowthFactor: %d\n", cfg.GrowthFactor)

	// create a cache
	myCache, err := cache.New(cfg.MaxCacheSize)
	if err != nil {
		fmt.Printf("error creating new cache: %v\n", err)
		return
	}

	// create aof
	aof, err := persistence.NewAOF(
		cfg.AOFFileName,
		cfg.SnapshotFileName,
		cfg.GetSyncPolicy(),
		myCache,
		cfg.GrowthFactor,
	)
	if err != nil {
		fmt.Printf("error creating new aof: %v\n", err)
		return
	}
	defer aof.Close()

	// recovery
	err = recoverAOF(myCache, aof, cfg.AOFFileName, cfg.SnapshotFileName)
	if err != nil {
		fmt.Printf("error recovering from aof: %v\n", err)
		return
	}

	// create node state
	nodeState := &NodeState{
		role: cfg.Role,
		leader: nil,
	}

	var leader *replication.Leader

	if cfg.Role == "leader" {
		leader, err = replication.NewLeader(myCache, aof, 0)
		if err != nil {
			fmt.Printf("error creating leader: %v\n", err)
			return
		}
		go leader.Start()
	} else {
		id := uuid.NewString()

		follower, err := replication.NewFollower(myCache, aof, cfg.LeaderAddr, id, []config.NodeInfo{}, 0, 0, nodeState) 
		if err != nil {
			fmt.Printf("error creating follower: %v\n", err)
		}
		go follower.Start()
	}
	
	// create a tcp listener on a port 
	address := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Printf("Error creating listener: %v", err)
		return
	}
	defer listener.Close()

	fmt.Printf("Listening on :%d...\n", cfg.Port)

	for {
		// accept an incoming connection
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Error accepting connection: %v", err)
			continue
		}

		// handle connection in a separate goroutine
		go handleConnection(conn, myCache, aof, nodeState)
	}
}

func startClusterMode(cfg *config.Config) {
	// get my node info
	myNode, err := cfg.GetMyNode()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// print values to verify
	fmt.Printf("Starting server with config:\n")
	fmt.Printf("MaxCacheSize: %d\n", cfg.MaxCacheSize)
	fmt.Printf("AOFFileName: %s\n", cfg.AOFFileName)
	fmt.Printf("SnapshotFileName: %s\n", cfg.SnapshotFileName)
	fmt.Printf("SyncPolicy: %s\n", cfg.SyncPolicy)
	fmt.Printf("SnapshotInterval: %v\n", cfg.SnapshotInterval)
	fmt.Printf("GrowthFactor: %d\n", cfg.GrowthFactor)

	fmt.Printf("My node info:\n")
	fmt.Printf("  ID: %s\n", myNode.ID)
	fmt.Printf("  Client port: %d\n", myNode.Port)
	fmt.Printf("  Replication port: %d\n", myNode.ReplPort)
	fmt.Printf("  Priority: %d\n", myNode.Priority)

	// show cluster topology
	fmt.Printf("\nCluster topology (%d nodes):\n", len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		marker := ""
		if node.ID == cfg.NodeID {
			marker = " <- ME"
		}
		fmt.Printf("  - %s (priority: %d, port: %d)%s\n", node.ID, node.Priority, node.Port, marker)
	}

	// create a cache
	myCache, err := cache.New(cfg.MaxCacheSize)
	if err != nil {
		fmt.Printf("error creating new cache: %v\n", err)
		return
	}

	// create aof
	aof, err := persistence.NewAOF(
		cfg.AOFFileName,
		cfg.SnapshotFileName,
		cfg.GetSyncPolicy(),
		myCache,
		cfg.GrowthFactor,
	)
	if err != nil {
		fmt.Printf("error creating new aof: %v\n", err)
		return
	}
	defer aof.Close()

	// recovery
	err = recoverAOF(myCache, aof, cfg.AOFFileName, cfg.SnapshotFileName)
	if err != nil {
		fmt.Printf("error recovering from aof: %v\n", err)
		return
	}

	// determine initial role based on priority
	// highest priority node starts as leader
	if cfg.AmIHighestPriority() {
		fmt.Printf("\n I have highest priority - starting as leader\n\n")
		startAsLeader(myNode, myCache, aof)
	} else {
		fmt.Printf("\n Not highest priority - starting as follower")

		// find the leader
		leaderNode := cfg.GetHighestPriorityNode()
		leaderAddr := fmt.Sprintf("%s:%d", leaderNode.Host, leaderNode.ReplPort)
		fmt.Printf("Connecting to leader %s at: %s\n\n", leaderNode.ID, leaderAddr)

		startAsFollower(myNode, myCache, aof, leaderAddr, cfg.Nodes)
	}
}

// helper function to start this node as a leader
func startAsLeader(myNode *config.NodeInfo, myCache *cache.Cache, aof *persistence.AOF) {
	// create node state
	nodeState := &NodeState{
		role: "leader",
		leader: nil,
	}
	
	// create leader
	leader, err := replication.NewLeader(myCache, aof, myNode.ReplPort) 
	if err != nil {
		fmt.Printf("error creating leader: %v\n", err)
		return
	}

	// set leader in node state
	nodeState.SetLeader(leader)

	go leader.Start()

	// start client listener
	startClientListener(myNode.Port, myCache, aof, nodeState)
}

// helper function to start this node as a follower
func startAsFollower(myNode *config.NodeInfo, myCache *cache.Cache, aof *persistence.AOF, leaderAddr string, clusterNodes []config.NodeInfo) {
	// create node state
	nodeState := &NodeState{
		role: "follower",
		leader: nil,
		leaderAddr: leaderAddr,
	}

	var leaderClientAddr string
	for _, node := range clusterNodes {
		// find leader node
		if node.Priority == getHighestPriority(clusterNodes) {
			leaderClientAddr = fmt.Sprintf("%s:%d", node.Host, node.Port)
			break
		}
	}

	nodeState.SetLeaderAddr(leaderClientAddr)
	
	// create follower
	follower, err := replication.NewFollower(myCache, aof, leaderAddr, myNode.ID, clusterNodes, myNode.Priority, myNode.ReplPort, nodeState) 
	if err != nil {
		fmt.Printf("error creating follower: %v\n", err)
		return
	}
	go follower.Start()

	// start client listener
	startClientListener(myNode.Port, myCache, aof, nodeState)
}

// helper function to start tcp listener for client connections
func startClientListener(port int, myCache *cache.Cache, aof *persistence.AOF, nodeState *NodeState) {
	address := fmt.Sprintf("0.0.0.0:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Printf("Error creating listener: %v", err)
		return
	}
	defer listener.Close()

	fmt.Printf("Listening for clients on :%d...\n", port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Error accepting connection: %v", err)
			continue
		}
		go handleConnection(conn, myCache, aof, nodeState)
	}
}

func getHighestPriority(nodes []config.NodeInfo) int {
	highest := 0
	
	for _, node := range nodes {
		if node.Priority > highest {
			highest = node.Priority
		}
	}
	
	return highest
}