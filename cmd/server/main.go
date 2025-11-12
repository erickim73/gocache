package main

import (
	"fmt"
	"net"
	"bufio"
	"github.com/erickim73/gocache/pkg/protocol"
	"github.com/erickim73/gocache/internal/cache"
)

func handleConnection(conn net.Conn, cache *cache.Cache) {
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
			if len(resultSlice) != 3 {
				conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
				continue
			}

			key := resultSlice[1].(string)
			value := resultSlice[2].(string)

			cache.Set(key, value, 0)

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
			conn.Write([]byte(protocol.EncodeSimpleString("OK")))
		} else {
			conn.Write([]byte(protocol.EncodeError("Unknown command " + command.(string))))
		}
	}

}

func main() {
	// create a tcp listener on port 6379
	listener, err := net.Listen("tcp", "0.0.0.0:6379")
	if err != nil {
		fmt.Println("Error creating listener:", err)
		return
	}
	defer listener.Close()

	fmt.Println("Listening on :6379...")

	myCache, err := cache.New(1000)
	if err != nil {
		fmt.Errorf("Error creating new cache: %v", err)
	}

	for {
		// accept an incoming connection
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}

		// handle connection in a separate goroutine
		go handleConnection(conn, myCache)
	}
}