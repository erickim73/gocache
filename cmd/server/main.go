package main

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"time"
	"sync"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/pkg/protocol"
	"github.com/google/uuid"
)

// NodeState holds mutable state that can change during runtime
type NodeState struct {
	role   string
	leader *replication.Leader
	mu     sync.RWMutex
}

// handle client commands and write to aof
func handleConnection(conn net.Conn, cache *cache.Cache, aof *persistence.AOF, leader *replication.Leader, role string) {
	defer conn.Close()

	// read from client
	reader := bufio.NewReader(conn)
	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			fmt.Println(err)
			return
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			fmt.Println("Error: result is not a slice")
			return
		}
		command := resultSlice[0]

		if command == "SET" {
			if len(resultSlice) < 3 || len(resultSlice) > 4 {
				conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
				continue
			}
			
			if role != "leader" {
				conn.Write([]byte(protocol.EncodeError("READONLY You can't write against a read only replica.")))
				continue
			}

			key := resultSlice[1].(string)
			value := resultSlice[2].(string)

			ttl := time.Duration(0)

			// if ttl provided as a 4th argument
			if len(resultSlice) == 4 {
				seconds := resultSlice[3].(string)
				ttlSec, err := strconv.Atoi(seconds)
				if err != nil {
					conn.Write([]byte(protocol.EncodeError("Couldn't convert seconds to a string")))
					continue
				}
				ttl = time.Duration(ttlSec) * time.Second
			}

			cache.Set(key, value, ttl)

			ttlSeconds := int64(ttl.Seconds()) // 0 if no TTL

			// send to followers
			if leader != nil {
				err := leader.Replicate(replication.OpSet, key, value, ttlSeconds)
				if err != nil {
					fmt.Printf("Error replicating SET command from leader to follower: %v\n", err)
				}
			}

			// write to aof
			ttlSecondsStr := strconv.FormatInt(ttlSeconds, 10) 
			aofCommand := protocol.EncodeArray([]interface{}{"SET", key, value, ttlSecondsStr})
			err := aof.Append(aofCommand)
			if err != nil {
				fmt.Printf("Failed to write to AOF: %v\n", err)
			}

			conn.Write([]byte(protocol.EncodeSimpleString("OK")))

		} else if command == "GET" {
			if len(resultSlice) != 2 {
				conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
				continue
			}

			key := resultSlice[1].(string)

			result, exists := cache.Get(key)

			if !exists {
				conn.Write([]byte(protocol.EncodeBulkString("", true)))
			} else {
				conn.Write([]byte(protocol.EncodeBulkString(result, false)))
			}
		} else if command == "DEL" {
			if len(resultSlice) != 2 {
				conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
				continue
			}

			if role != "leader" {
				conn.Write([]byte(protocol.EncodeError("READONLY You can't write against a read only replica.")))
				continue
			}

			key := resultSlice[1].(string)

			cache.Delete(key)

			// send to followers
			if leader != nil {
				err := leader.Replicate(replication.OpDelete, key, "", 0)
				if err != nil {
					fmt.Printf("Error replicating DEL command from leader to follower: %v\n", err)
				}
			}

			aofCommand := protocol.EncodeArray([]interface{}{"DEL", key})
			err := aof.Append(aofCommand)
			if err != nil {
				fmt.Printf("Failed to write to AOF: %v\n", err)
			}

			conn.Write([]byte(protocol.EncodeSimpleString("OK")))
		} else {
			conn.Write([]byte(protocol.EncodeError("Unknown command " + command.(string))))
		}
	}
}

// reads aof and applies operations to cache
func recoverAOF(cache *cache.Cache, aof *persistence.AOF, aofName string, snapshotName string) error {
	if persistence.SnapshotExists(snapshotName) {
		err := aof.LoadSnapshot()
		if err != nil {
			return fmt.Errorf("error loading snapshot: %v", err)
		}
		fmt.Println("Loaded snapshot successfully")
	}

	if persistence.AofExists(aofName) {
		ops, err := aof.ReadOperations()
		if err != nil {
			return fmt.Errorf("error reading operations from aov: %v", err)
		}
		fmt.Printf("Found %d operations from aof to recover\n", len(ops))

		for _, op := range ops {
			if op.Type == "SET" {
				ttl := time.Duration(op.TTL) * time.Second
				err := cache.Set(op.Key, op.Value, ttl)
				if err != nil {
					return fmt.Errorf("error applying set operation on cache")
				}
			} else if op.Type == "DEL" {
				err := cache.Delete(op.Key)
				if err != nil {
					return fmt.Errorf("error deleting key from cache")
				}
			}
		}
		fmt.Printf("Successfully recovered %d operations from aof\n", len(ops))
	}

	return nil

}

func main() {
	// load defaults
	cfg := config.DefaultConfig()

	// parse flags
	configFile := config.ParseFlags(cfg)

	// load config file
	fileCfg, err := config.LoadFromFile(configFile)
	if err != nil {
		fmt.Printf("Could not load config file '%s', using defaults: %v\n", configFile, err)
	} else {
		cfg = fileCfg
	}

	// apply flags after loading file
	config.ApplyFlags(cfg)

	// determine which mode we're in 
	if cfg.IsClusterMode() {
		fmt.Println("Starting in CLUSTER mode")
		startClusterMode(cfg)
	} else {
		fmt.Println("Starting in SIMPLE mode")
		startSimpleMode(cfg)
	}

	
}

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

		follower, err := replication.NewFollower(myCache, aof, cfg.LeaderAddr, id, []config.NodeInfo{}, 0, 0) 
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
		go handleConnection(conn, myCache, aof, leader, cfg.Role)
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
	// create leader
	leader, err := replication.NewLeader(myCache, aof, myNode.ReplPort) 
	if err != nil {
		fmt.Printf("error creating leader: %v\n", err)
		return
	}
	go leader.Start()

	// start client listener
	startClientListener(myNode.Port, myCache, aof, leader, "leader")
}

// helper function to start this node as a follower
func startAsFollower(myNode *config.NodeInfo, myCache *cache.Cache, aof *persistence.AOF, leaderAddr string, clusterNodes []config.NodeInfo) {
	// create follower
	follower, err := replication.NewFollower(myCache, aof, leaderAddr, myNode.ID, clusterNodes, myNode.Priority, myNode.ReplPort) 
	if err != nil {
		fmt.Printf("error creating follower: %v\n", err)
		return
	}
	go follower.Start()

	// start client listener
	startClientListener(myNode.Port, myCache, aof, nil, "follower")
}

// helper function to start tcp listener for client connections
func startClientListener(port int, myCache *cache.Cache, aof *persistence.AOF, leader *replication.Leader, role string) {
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
		go handleConnection(conn, myCache, aof, leader, role)
	}
}