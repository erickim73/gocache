package main

import (
	"fmt"
	"net"
	"bufio"
	"github.com/erickim73/gocache/pkg/protocol"
)

func handleConnection(conn net.Conn) {
	defer conn.Close()

	// read from client
	reader := bufio.NewReader(conn)
	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			fmt.Println(err)
			return
		}
		
		message := fmt.Sprintf("%v", result)
		fmt.Println("Received", result)

		// echo message back to client
		_, err = conn.Write([]byte("Echo: " + message))
		if err != nil {
			fmt.Println("Err writing", err)
			return
		}
	}

}

func main() {
	// create a tcp listener on port 6379
	listener, err := net.Listen("tcp", ":6379")
	if err != nil {
		fmt.Println("Error creating listener:", err)
		return
	}
	defer listener.Close()

	fmt.Println("Listening on :6379...")

	for {
		// accept an incoming connection
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}

		// handle connection in a separate goroutine
		go handleConnection(conn)
	}
}