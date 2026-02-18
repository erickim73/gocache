package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/cluster"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/metrics"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/pubsub"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/google/uuid"
)

var (
	packageMetricsCollector *metrics.Collector
	packageMetricsOnce      sync.Once
)

// get or create shared collector
func getMetricsCollector() *metrics.Collector {
	packageMetricsOnce.Do(func() {
		packageMetricsCollector = metrics.NewCollector()
	})
	return packageMetricsCollector
}

type Server struct {
	cfg *config.Config
	listener net.Listener
	aof *persistence.AOF
	leader *replication.Leader
	cancel context.CancelFunc
	wg sync.WaitGroup
}

// creates a server ready to be started
func New(cfg *config.Config) *Server {
	return &Server{cfg: cfg}
}

// runs the server and blocks until stop() is called
func (s *Server) Start() {
	cfg := s.cfg

	slog.Info("Starting simple mode server",
		"port", cfg.Port,
		"max_cache_size", cfg.MaxCacheSize,
		"aof_file", cfg.AOFFileName,
		"snapshot_file", cfg.SnapshotFileName,
		"sync_policy", cfg.SyncPolicy,
		"snapshot_interval", cfg.SnapshotInterval,
		"growth_factor", cfg.GrowthFactor,
	)

	// create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	defer cancel()

	// create a cache
	metricsCollector := getMetricsCollector()
	myCache, err := cache.NewCache(cfg.MaxCacheSize, metricsCollector)
	if err != nil {
		slog.Error("Failed to create cache", "error", err)
		return
	}

	// create aof
	s.aof, err = persistence.NewAOF(
		cfg.AOFFileName,
		cfg.SnapshotFileName,
		cfg.GetSyncPolicy(),
		myCache,
		cfg.GrowthFactor,
	)
	if err != nil {
		slog.Error("Failed to create AOF", "error", err)
		return
	}

	// recovery
	err = persistence.RecoverAOF(myCache, s.aof, cfg.AOFFileName, cfg.SnapshotFileName)
	if err != nil {
		slog.Error("Failed to recover from AOF", "error", err)
		return
	}

	// create pubsub
	ps := pubsub.NewPubSub()

	// create node state
	nodeState, err := NewNodeState(cfg.Role, nil, "")
	if err != nil {
		slog.Error("Failed to create node state", "error", err)
	}

	if cfg.Role == "leader" {
		replPort := cfg.Port + 1000
		s.leader, err = replication.NewLeader(myCache, s.aof, replPort)
		if err != nil {
			slog.Error("Failed to create leader", "error", err)
			return
		}
		go s.leader.Start()
		slog.Info("Started as leader")
	} else {
		id := uuid.NewString()

		follower, err := replication.NewFollower(myCache, s.aof, cfg.LeaderAddr, id, []config.NodeInfo{}, 0, 0, nodeState) 
		if err != nil {
			slog.Error("Failed to create follower", "error", err)
		}
		go follower.Start()
		slog.Info("Started as follower", "leader_address", cfg.LeaderAddr)
	}	
	
	// create a tcp listener on a port 
	address := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		slog.Error("Failed to create listener", "address", address, "error", err)
		return
	}

	s.listener = listener

	slog.Info("Cache server listening", "address", address)

	// register with WaitGroup so Stop() can block until accept loop has actually exited before returning to caller
	s.wg.Add(1)
	defer s.wg.Done()


	for {
		// accept an incoming connection
		conn, err := listener.Accept()
		if err != nil {
			// check context before deciding what to do with error
			select {
			case <- ctx.Done():
				slog.Info("Server shutting down cleanly")
				return // intentional stop
			default:
				slog.Warn("Error accepting connection", "error", err)
				continue
			}
		}

		// handle connection in a separate goroutine
		go handleConnection(conn, myCache, s.aof, nodeState, ps)
	}
}

// signals Start() to exit and waits until it has
func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel() // signal the accept loop that this is an intentional stop
	}

	// close leader's replication listener first
	if s.leader != nil {
		if err := s.leader.Close(); err != nil {
			slog.Warn("Error closing leader listener", "error", err)
		}
	}

	if s.listener != nil {
		s.listener.Close()
	}

	if s.listener != nil {
		s.listener.Close() // unblock any pending Accept() call
	}

	// close AOF before waiting for accept loop to finish
	if s.aof != nil {
		if err := s.aof.Close(); err != nil {
			slog.Warn("Error closing AOF", "error", err)
		}
	}

	s.wg.Wait() // don't return  until the accept loop has fully exited
}

func StartClusterMode(cfg *config.Config) {
	// get my node info
	myNode, err := cfg.GetMyNode()
	if err != nil {
		// node not in config -start in "pending join" mode
		slog.Info("Node not found in initial cluster config",
			"node_id", cfg.NodeID, 
			"mode", "PENDING")     
		startPendingNode(cfg)
		return
	}

	// print values to verify
	slog.Info("Starting cluster mode server",
		"max_cache_size", cfg.MaxCacheSize,
		"aof_file", cfg.AOFFileName,
		"snapshot_file", cfg.SnapshotFileName,
		"sync_policy", cfg.SyncPolicy,
		"snapshot_interval", cfg.SnapshotInterval,
		"growth_factor", cfg.GrowthFactor,
	)
	slog.Info("My node info",
		"id", myNode.ID,
		"client_port", myNode.Port,
		"replication_port", myNode.ReplPort,
		"priority", myNode.Priority,
	)

	// show cluster topology
	slog.Info("Cluster topology", "num_nodes", len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		marker := ""
		if node.ID == cfg.NodeID {
			marker = " <- ME"
		}
		slog.Info("Cluster node",
			"node_id", node.ID,
			"priority", node.Priority,
			"port", node.Port,
			"marker", marker,
		)
	}

	// create a cache
	metricsCollector := getMetricsCollector()
	myCache, err := cache.NewCache(cfg.MaxCacheSize, metricsCollector)
	if err != nil {
		slog.Error("Error creating new cache", "error", err)
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
		slog.Error("Error creating new AOF", "error", err)
		return
	}
	defer aof.Close()

	// recovery
	err = persistence.RecoverAOF(myCache, aof, cfg.AOFFileName, cfg.SnapshotFileName)
	if err != nil {
		slog.Error("Error recovering from AOF", "error", err)
		return
	}

	// create hash ring for key distribution
	slog.Info("Initializing Hash Ring for Key Distribution")
	hashRing := cluster.NewHashRing(150)

	// add all cluster nodes to hash ring
	for _, shard := range cfg.Shards {
		hashRing.AddShard(shard.ShardID)
		slog.Info("Added shard to hash ring", "shard_id", shard.ShardID)

		// map shard to its nodes [leader, follower1, follower2...]
		nodes := []string{shard.LeaderID}
		nodes = append(nodes, shard.Followers...)
		hashRing.SetShardNodes(shard.ShardID, nodes)
		slog.Info("Shard nodes configured",
			"shard_id", shard.ShardID,
			"nodes", nodes,
		)
	}

	slog.Info("Hash ring initialized",
		"num_nodes", len(cfg.Nodes),
		"virtual_nodes_per_node", 150,
	)

	// determine initial role based on priority
	// highest priority node starts as leader
	if cfg.IsShardLeader() {
		slog.Info("Starting as leader", "reason", "highest priority in shard")
		startAsLeader(myNode, myCache, aof, cfg, hashRing)
	} else {
		slog.Info("Starting as follower", "reason", "not highest priority in shard")

		// find shard's leader
		myShard, err := cfg.GetShardForNode(cfg.NodeID)
		if err != nil {
			slog.Error("Cannot find my shard", "error", err)
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
			slog.Error("Cannot find shard leader node info")
			return
		}

		leaderAddr := fmt.Sprintf("%s:%d", leaderNode.Host, leaderNode.ReplPort)
		slog.Info("Connecting to shard leader",
			"leader_id", leaderNode.ID,
			"leader_addr", leaderAddr,
		)

		startAsFollower(myNode, myCache, aof, leaderAddr, cfg.Nodes, cfg, hashRing)
	}
}

// helper function to start this node as a leader
func startAsLeader(myNode *config.NodeInfo, myCache *cache.Cache, aof *persistence.AOF, cfg *config.Config, hashRing *cluster.HashRing) {
	// create node state
	nodeState, err := NewNodeState("leader", nil, "")
	if err != nil {
		slog.Error("Error creating node state", "error", err)
		return
	}	

	// set cluster components in node state for routing
	nodeState.SetConfig(cfg)
	nodeState.SetHashRing(hashRing)
	slog.Info("Cluster routing enabled", "components", "hash_ring + config")

	// set node addresses in hash ring
	slog.Info("Setting Node Addresses in Hash Ring")
	for _, node := range cfg.Nodes {
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		hashRing.SetNodeAddress(node.ID, nodeAddr)
		slog.Info("Set node address",
			"node_id", node.ID,
			"address", nodeAddr,
		)
	}

	// create migrator and set it in node state
	slog.Info("Initializing Migrator")
	migrator := cluster.NewMigrator(myCache, hashRing)
	nodeState.SetMigrator(migrator)
	nodeState.SetCache(myCache)
	slog.Info("Migrator initialized")

	// create and configure health check
	slog.Info("Initializing Health Checker", "role", "leader")
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
			slog.Info("Monitoring node",
				"node_id", node.ID,
				"address", nodeAddr,
			)
		}
	}

	// set callbacks for node failure/recovery
	healthChecker.SetCallbacks(
		func(failedNodeID string) {
			slog.Warn("Node failed, removing from hash ring", "node_id", failedNodeID)
			hashRing.RemoveShard(failedNodeID)
			hashRing.SetNodeAddress(failedNodeID, "")
			slog.Info("Hash ring updated after node failure", "removed_node", failedNodeID)
		},
		func(recoveredNodeID string) {
			slog.Info("Node recovered, adding back to hash ring", "node_id", recoveredNodeID)

			// find the node's address from config
			for _, node := range cfg.Nodes {
				if node.ID == recoveredNodeID {
					nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
					hashRing.AddShard(recoveredNodeID)
					hashRing.SetNodeAddress(recoveredNodeID, nodeAddr)
					slog.Info("Hash ring updated after node recovery",
						"recovered_node", recoveredNodeID,
						"address", nodeAddr,
					)
				}
			}
		},
	)

	nodeState.SetHealthChecker(healthChecker)

	// start health checking
	healthChecker.Start()
	slog.Info("Health checker started")

	// create leader
	leader, err := replication.NewLeader(myCache, aof, myNode.ReplPort) 
	if err != nil {
		slog.Error("Error creating leader", "error", err)
		return
	}

	// set leader in node state
	nodeState.SetLeader(leader)

	go leader.Start()

	slog.Info("Setting Up Shard Replication")
	myShard, err := cfg.GetShardForNode(cfg.NodeID)
	if err == nil && myShard.LeaderID == cfg.NodeID {
		slog.Info("Leading shard",
			"shard_id", myShard.ShardID,
			"followers", myShard.Followers,
		)
	} else if err != nil {
		slog.Warn("Could not determine my shard", "error", err)
	}

	// start client listener
	startClientListener(myNode.Port, myCache, aof, nodeState)
}

// helper function to start this node as a follower
func startAsFollower(myNode *config.NodeInfo, myCache *cache.Cache, aof *persistence.AOF, leaderAddr string, clusterNodes []config.NodeInfo, cfg *config.Config, hashRing *cluster.HashRing) {
	// create node state
	nodeState, err := NewNodeState("follower", nil, leaderAddr)
	if err != nil {
		slog.Error("Error creating node state", "error", err)
		return
	}

	// set cluster components in node state for routing
	nodeState.SetConfig(cfg)
	nodeState.SetHashRing(hashRing)

	// set node addresses in hash ring
	slog.Info("Setting Node Addresses in Hash Ring")
	for _, node := range cfg.Nodes {
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		hashRing.SetNodeAddress(node.ID, nodeAddr)
		slog.Info("Set node address",
			"node_id", node.ID,
			"address", nodeAddr,
		)
	}

	// create migrator and set it in node state
	slog.Info("Initializing Migrator")
	migrator := cluster.NewMigrator(myCache, hashRing)
	nodeState.SetMigrator(migrator)
	nodeState.SetCache(myCache)
	slog.Info("Migrator initialized")

	slog.Info("Cluster routing enabled", "components", "hash_ring + config")

	// create and configure health check
	slog.Info("Initializing Health Checker", "role", "follower")
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
			slog.Info("Monitoring node",
				"node_id", node.ID,
				"address", nodeAddr,
			)
		}
	}

	// follower callbacks for node failure/recovery
	healthChecker.SetCallbacks(
		func(failedNodeID string) {
			slog.Warn("Detected node failure",
				"role", "follower",
				"failed_node", failedNodeID,
			)
		},
		func(recoveredNodeID string) {
			slog.Info("Detected node recovery",
				"role", "follower",
				"recovered_node", recoveredNodeID,
			)
		},
	)

	nodeState.SetHealthChecker(healthChecker)

	// start health checking
	healthChecker.Start()
	slog.Info("Health checker started")

	slog.Info("Setting Up Shard Replication")
	myShard, err := cfg.GetShardForNode(cfg.NodeID)
	if err != nil {
		slog.Error("Error finding my shard", "error", err)
	} else {
		slog.Info("Follower in shard",
			"shard_id", myShard.ShardID,
			"shard_leader", myShard.LeaderID,
		)
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
		slog.Warn("Could not find shard leader replication address, using fallback")
		shardLeaderReplAddr = leaderAddr // fallback
	}

	slog.Info("Connecting to shard leader", "address", shardLeaderReplAddr)

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
		slog.Error("Error creating follower", "error", err)
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
		slog.Error("Error creating listener", "error", err, "address", address)
		return
	}
	defer listener.Close()

	ps := pubsub.NewPubSub()

	slog.Info("Listening for clients", "port", port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			slog.Warn("Error accepting connection", "error", err)
			continue
		}
		go handleConnection(conn, myCache, aof, nodeState, ps)
	}
}

// helper function to add this pending node
func startPendingNode(cfg *config.Config) {
	slog.Info("Starting server with config",
		"max_cache_size", cfg.MaxCacheSize,
		"aof_file", cfg.AOFFileName,
		"snapshot_file", cfg.SnapshotFileName,
		"node_id", cfg.NodeID,
		"status", "PENDING - waiting to join cluster",
	)

	// create cache
	metricsCollector := getMetricsCollector()
	myCache, err := cache.NewCache(cfg.MaxCacheSize, metricsCollector)
	if err != nil {
		slog.Error("Error creating new cache", "error", err)
		return
	}

	// create AOF
	aof, err := persistence.NewAOF(cfg.AOFFileName, cfg.SnapshotFileName, cfg.GetSyncPolicy(), myCache, cfg.GrowthFactor)
	if err != nil {
		slog.Error("Error creating new AOF", "error", err)
		return
	}
	defer aof.Close()

	// recovery
	err = persistence.RecoverAOF(myCache, aof, cfg.AOFFileName, cfg.SnapshotFileName)
	if err != nil {
		slog.Error("Error recovering from AOF", "error", err)
		return
	}

	// create hash ring with just the known nodes
	slog.Info("Initializing Hash Ring")
	hashRing := cluster.NewHashRing(150)

	// add nodes from config
	for _, node := range cfg.Nodes {
		hashRing.AddShard(node.ID)
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		hashRing.SetNodeAddress(node.ID, nodeAddr)
		slog.Info("Added node to hash ring",
			"node_id", node.ID,
			"address", nodeAddr,
		)
	}
	slog.Info("Hash ring initialized", "num_nodes", len(cfg.Nodes))

	// create node state (no replication yet)
	nodeState, err := NewNodeState("pending", nil, "")
	if err != nil {
		slog.Error("Error creating node state", "error", err)
		return
	}

	// set cluster components
	nodeState.SetConfig(cfg)
	nodeState.SetHashRing(hashRing)

	// create migrator
	migrator := cluster.NewMigrator(myCache, hashRing)
	nodeState.SetMigrator(migrator)
	nodeState.SetCache(myCache)

	slog.Info("Node ready to join cluster")

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