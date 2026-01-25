package main

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/pkg/protocol"
	"github.com/google/uuid"
)

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
		leader, err = replication.NewLeader(myCache, aof)
		if err != nil {
			fmt.Printf("error creating leader: %v\n", err)
			return
		}
		go leader.Start()
	} else {
		id := uuid.NewString()

		follower, err := replication.NewFollower(myCache, cfg.LeaderAddr, id) 
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
