package main

import (
	"fmt"
	"net"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/cluster"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/internal/server"
	"github.com/erickim73/gocache/internal/metrics"
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
	metricsCollector := metrics.NewCollector()
	myCache, err := cache.NewCache(cfg.MaxCacheSize, metricsCollector)
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
	nodeState, err := server.NewNodeState(cfg.Role, nil, "")

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
		// node not in config -s tart in "pending join" mode
		fmt.Printf("Node %s not found in initial cluster config\n", cfg.NodeID)
		fmt.Printf("Starting in PENDING mode -waiting to be added via CLUSTER ADDNODE\n")
		startPendingNode(cfg)
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
	metricsCollector := metrics.NewCollector()
	myCache, err := cache.NewCache(cfg.MaxCacheSize, metricsCollector)
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

	// create hash ring for key distribution
	fmt.Println("\n=== Initializing Hash Ring for Key Distribution === ")
	hashRing := cluster.NewHashRing(150)

	// add all cluster nodes to hash ring
	for _, shard := range cfg.Shards {
		hashRing.AddShard(shard.ShardID)
		fmt.Printf("  ✓ Added shard to hash ring: %s\n", shard.ShardID)

		// map shard to its nodes [leader, follower1, follower2...]
		nodes := []string{shard.LeaderID}
		nodes = append(nodes, shard.Followers...)
		hashRing.SetShardNodes(shard.ShardID, nodes)
		fmt.Printf("    Shard %s nodes: %v\n", shard.ShardID, nodes)
	}

	fmt.Printf("✓ Hash ring initialized with %d nodes\n", len(cfg.Nodes))
	fmt.Printf("  Each node has 150 virtual nodes for balanced distribution\n")
	fmt.Println("==========================================")

	// determine initial role based on priority
	// highest priority node starts as leader
	if cfg.IsShardLeader() {
		fmt.Printf("\n I have highest priority - starting as leader\n\n")
		startAsLeader(myNode, myCache, aof, cfg, hashRing)
	} else {
		fmt.Printf("\n I am a shared follower - starting as follower")

		// find shard's leader
		myShard, err := cfg.GetShardForNode(cfg.NodeID)
		if err != nil {
			fmt.Printf("Error: Cannot find my shard: %v\n", err)
			return
		}

		// find the leader node info
		var leaderNode *config.NodeInfo
		for i := range cfg.Nodes {
			if cfg.Nodes[i].ID == myShard.LeaderID {
				leaderNode = &cfg.Nodes[i]
				break
			}
		}

		if leaderNode == nil {
			fmt.Printf("Error: Cannot find shard leader node info\n")
			return
		}

		leaderAddr := fmt.Sprintf("%s:%d", leaderNode.Host, leaderNode.ReplPort)
		fmt.Printf("Connecting to shard leader %s at: %s\n\n", leaderNode.ID, leaderAddr)

		startAsFollower(myNode, myCache, aof, leaderAddr, cfg.Nodes, cfg, hashRing)
	}
}

// helper function to start this node as a leader
func startAsLeader(myNode *config.NodeInfo, myCache *cache.Cache, aof *persistence.AOF, cfg *config.Config, hashRing *cluster.HashRing) {
	// create node state
	nodeState, err := server.NewNodeState("leader", nil, "")
	if err != nil {
		fmt.Printf("error creating node state: %v\n", err)
		return
	}	

	// set cluster components in node state for routing
	nodeState.SetConfig(cfg)
	nodeState.SetHashRing(hashRing)
	fmt.Println("✓ Cluster routing enabled (hash ring + config)")

	// set node addresses in hash ring
	fmt.Println("\n=== Setting Node Addresses in Hash Ring ===")
	for _, node := range cfg.Nodes {
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		hashRing.SetNodeAddress(node.ID, nodeAddr)
		fmt.Printf("  ✓ Set address for %s: %s\n", node.ID, nodeAddr)
	}
	fmt.Println("========================================")

	// create migrator and set it in node state
	fmt.Println("\n=== Initializing Migrator ===")
	migrator := cluster.NewMigrator(myCache, hashRing)
	nodeState.SetMigrator(migrator)
	nodeState.SetCache(myCache)
	fmt.Println("✓ Migrator initialized")
	fmt.Println("=============================")

	// create and configure health check
	fmt.Println("\n=== Initializing Health Checker (leader) ===")
	healthChecker := cluster.NewHealthChecker(
		hashRing, 
		5 * time.Second, // check every 5 sec
		3, // 3 failures before dead
		2 * time.Second, // 2 second timeout per ping
	)

	// register all nodes from config
	for _, node := range cfg.Nodes {
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)

		// don't health check node itself
		if node.ID != cfg.NodeID {
			healthChecker.RegisterNode(node.ID, nodeAddr)
			fmt.Printf("  ✓ Monitoring node %s at %s\n", node.ID, nodeAddr)
		}
	}

	// set callbacks for node failure/recovery
	healthChecker.SetCallbacks(
		func(failedNodeID string) {
			fmt.Printf("[CLUSTER] Node %s failed! Removing from hash ring...\n", failedNodeID)
			hashRing.RemoveShard(failedNodeID)
			hashRing.SetNodeAddress(failedNodeID, "")
			fmt.Printf("[CLUSTER] Hash ring updated - node %s removed\n", failedNodeID)
		},
		func(recoveredNodeID string) {
			fmt.Printf("[CLUSTER] Node %s recovered! Adding back to hash ring...\n", recoveredNodeID)

			// find the node's address from config
			for _, node := range cfg.Nodes {
				if node.ID == recoveredNodeID {
					nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
					hashRing.AddShard(recoveredNodeID)
					hashRing.SetNodeAddress(recoveredNodeID, nodeAddr)
					fmt.Printf("[CLUSTER] Hash ring updated - node %s added back\n", recoveredNodeID)
				}
			}
		},
	)

	nodeState.SetHealthChecker(healthChecker)

	// start health checking
	healthChecker.Start()
	fmt.Println("✓ Health checker started")
	fmt.Println("====================================")

	fmt.Println("✓ Cluster routing enabled (hash ring + config)")

	// create leader
	leader, err := replication.NewLeader(myCache, aof, myNode.ReplPort) 
	if err != nil {
		fmt.Printf("error creating leader: %v\n", err)
		return
	}

	// set leader in node state
	nodeState.SetLeader(leader)

	go leader.Start()

	fmt.Println("\n=== Setting Up Shard Replication ===")
	myShard, err := cfg.GetShardForNode(cfg.NodeID)
	if err == nil && myShard.LeaderID == cfg.NodeID {
		fmt.Printf("I am leader of shard %s\n", myShard.ShardID)
		fmt.Printf("Waiting for followers: %v\n", myShard.Followers)
	} else if err != nil {
		fmt.Printf("Warning: Could not determine my shard: %v\n", err)
	}
	fmt.Println("===================================")

	// start client listener
	startClientListener(myNode.Port, myCache, aof, nodeState)
}

// helper function to start this node as a follower
func startAsFollower(myNode *config.NodeInfo, myCache *cache.Cache, aof *persistence.AOF, leaderAddr string, clusterNodes []config.NodeInfo, cfg *config.Config, hashRing *cluster.HashRing) {
	// create node state
	nodeState, err := server.NewNodeState("follower", nil, leaderAddr)
	if err != nil {
		fmt.Printf("error creating node state: %v\n", err)
		return
	}

	// set cluster components in node state for routing
	nodeState.SetConfig(cfg)
	nodeState.SetHashRing(hashRing)

	// set node addresses in hash ring
	fmt.Println("\n=== Setting Node Addresses in Hash Ring ===")
	for _, node := range cfg.Nodes {
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		hashRing.SetNodeAddress(node.ID, nodeAddr)
		fmt.Printf("  ✓ Set address for %s: %s\n", node.ID, nodeAddr)
	}
	fmt.Println("========================================")

	// create migrator and set it in node state
	fmt.Println("\n=== Initializing Migrator ===")
	migrator := cluster.NewMigrator(myCache, hashRing)
	nodeState.SetMigrator(migrator)
	nodeState.SetCache(myCache)
	fmt.Println("✓ Migrator initialized")
	fmt.Println("=============================")

	fmt.Println("✓ Cluster routing enabled (hash ring + config)")

	// create and configure health check
	fmt.Println("\n=== Initializing Health Checker (follower) ===")
	healthChecker := cluster.NewHealthChecker(
		hashRing, 
		5 * time.Second, // check every 5 sec
		3, // 3 failures before dead
		2 * time.Second, // 2 second timeout per ping
	)

	// get leader nod ID to exclude from health checking
	leaderNode := cfg.GetHighestPriorityNode()

	// register all nodes from config except leader and self
	for _, node := range cfg.Nodes {
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)

		// skip self and leader
		if node.ID != cfg.NodeID && node.ID != leaderNode.ID {
			healthChecker.RegisterNode(node.ID, nodeAddr)
			fmt.Printf("  ✓ Monitoring node %s at %s\n", node.ID, nodeAddr)
		}
	}

	// follower callbacks for node failure/recovery
	healthChecker.SetCallbacks(
		func(failedNodeID string) {
			fmt.Printf("[CLUSTER][FOLLOWER] Detected node %s failure\n", failedNodeID)
		},
		func(recoveredNodeID string) {
			fmt.Printf("[CLUSTER][FOLLOWER] Detected node %s recovery\n", recoveredNodeID)
		},
	)

	nodeState.SetHealthChecker(healthChecker)

	// start health checking
	healthChecker.Start()
	fmt.Println("✓ Health checker started")
	fmt.Println("====================================")

	fmt.Println("\n=== Setting Up Shard Replication ===")
	myShard, err := cfg.GetShardForNode(cfg.NodeID)
	if err != nil {
		fmt.Printf("Error finding my shard: %v\n", err)
	} else {
		fmt.Printf("I am follower in shard %s\n", myShard.ShardID)
		fmt.Printf("My shard leader: %s\n", myShard.LeaderID)
	}

	// find my shard leader's replication address
	var shardLeaderReplAddr string
	if myShard != nil {
		for _, node := range cfg.Nodes {
			if node.ID == myShard.LeaderID {
				shardLeaderReplAddr = fmt.Sprintf("%s:%d", node.Host, node.ReplPort)
				break
			}
		}
	}

	if shardLeaderReplAddr == "" {
		fmt.Printf("Warning: Could not find shard leader replication address, using default\n")
		shardLeaderReplAddr = leaderAddr // fallback
	}

	fmt.Printf("Connecting to shard leader at %s\n", shardLeaderReplAddr)
	fmt.Println("===========================")

	// find leader's client address for forwarding
	var leaderClientAddr string
	if myShard != nil {
		for _, node := range cfg.Nodes {
			if node.ID == myShard.LeaderID {
				leaderClientAddr = fmt.Sprintf("%s:%d", node.Host, node.Port)
				break
			}
		}
	}

	nodeState.SetLeaderAddr(leaderClientAddr)
	
	// create follower
	follower, err := replication.NewFollower(myCache, aof, shardLeaderReplAddr, myNode.ID, clusterNodes, myNode.Priority, myNode.ReplPort, nodeState) 
	if err != nil {
		fmt.Printf("error creating follower: %v\n", err)
		return
	}
	go follower.Start()

	// start client listener
	startClientListener(myNode.Port, myCache, aof, nodeState)
}

// helper function to start tcp listener for client connections
func startClientListener(port int, myCache *cache.Cache, aof *persistence.AOF, nodeState *server.NodeState) {
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

// helper function to add this pending node
func startPendingNode(cfg *config.Config) {
	fmt.Printf("Starting server with config:\n")
	fmt.Printf("MaxCacheSize: %d\n", cfg.MaxCacheSize)
	fmt.Printf("AOFFileName: %s\n", cfg.AOFFileName)
	fmt.Printf("SnapshotFileName: %s\n", cfg.SnapshotFileName)

	fmt.Printf("\nNodeID: %s\n", cfg.NodeID)
	fmt.Printf("Status: PENDING - waiting to join cluster\n")

	// create cache
	metricsCollector := metrics.NewCollector()
	myCache, err := cache.NewCache(cfg.MaxCacheSize, metricsCollector)
	if err != nil {
		fmt.Printf("error creating new cache: %v\n", err)
		return
	}

	// create AOF
	aof, err := persistence.NewAOF(cfg.AOFFileName, cfg.SnapshotFileName, cfg.GetSyncPolicy(), myCache, cfg.GrowthFactor)
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

	// create hash ring with just the known nodes
	fmt.Println("\n=== Initializing Hash Ring ===")
	hashRing := cluster.NewHashRing(150)

	// add nodes from config
	for _, node := range cfg.Nodes {
		hashRing.AddShard(node.ID)
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		hashRing.SetNodeAddress(node.ID, nodeAddr)
		fmt.Printf("  ✓ Added node: %s at %s\n", node.ID, nodeAddr)
	}
	fmt.Printf("✓ Hash ring initialized with %d nodes\n", len(cfg.Nodes))
	fmt.Println("=========================")

	// create node state (no replication yet)
	nodeState, err := server.NewNodeState("pending", nil, "")
	if err != nil {
		fmt.Printf("error creating node state: %v\n", err)
		return
	}

	// set cluster components
	nodeState.SetConfig(cfg)
	nodeState.SetHashRing(hashRing)

	// create migrator
	migrator := cluster.NewMigrator(myCache, hashRing)
	nodeState.SetMigrator(migrator)
	nodeState.SetCache(myCache)

	fmt.Println("✓ Node ready to join cluster")

	// determine port - use first available port or default
	port := 8382

	// for now, check if there's a port set at the top level
	if cfg.Port > 0 {
		port = cfg.Port
	}

	// start client listener
	startClientListener(port, myCache, aof, nodeState)
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