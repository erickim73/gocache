package main

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/pkg/protocol"
)

// handle client commands and write to aof
func handleConnection(conn net.Conn, cache *cache.Cache, aof *persistence.AOF) {
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

			key := resultSlice[1].(string)
			value := resultSlice[2].(string)

			ttl := time.Duration(0)

			// if ttl provided as a 4th argument
			if len(resultSlice) == 4 {
				seconds := resultSlice[3].(string)
				ttlSeconds, err := strconv.Atoi(seconds)
				if err != nil {
					conn.Write([]byte(protocol.EncodeError("Couldn't convert seconds to a string")))
					continue
				}
				ttl = time.Duration(ttlSeconds) * time.Second
			}

			cache.Set(key, value, ttl)

			// write to aof
			ttlSeconds := strconv.Itoa(int(ttl.Seconds()))
			aofCommand := protocol.EncodeArray([]string{"SET", key, value, ttlSeconds})
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

			key := resultSlice[1].(string)

			cache.Delete(key)

			aofCommand := protocol.EncodeArray([]string{"DEL", key})
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
		fmt.Printf("Successfully recovered %d operations\n", len(ops))
	}

	return nil

}

func main() {
	// create a cache
	myCache, err := cache.New(1000)
	if err != nil {
		fmt.Printf("error creating new cache: %v\n", err)
		return
	}

	// create aof
	aof, err := persistence.NewAOF("cache.aof", "cache.rdb", persistence.SyncEverySecond, myCache, 100)
	if err != nil {
		fmt.Printf("error creating new aof: %v\n", err)
		return
	}
	defer aof.Close()


	err = recoverAOF(myCache, aof, "cache.aof", "cache.rdb")
	if err != nil {
		fmt.Printf("error recovering from aof: %v\n", err)
		return
	}
	
	// create a tcp listener on port 6379
	listener, err := net.Listen("tcp", "0.0.0.0:6379")
	if err != nil {
		fmt.Printf("Error creating listener: %v", err)
		return
	}
	defer listener.Close()

	fmt.Printf("Listening on :6379...")

	

	for {
		// accept an incoming connection
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Error accepting connection: %v", err)
			continue
		}

		// handle connection in a separate goroutine
		go handleConnection(conn, myCache, aof)
	}
}