package main

import (
	"fmt"
	"net"
)

func handleConnection(conn net.Conn) {
	defer conn.Close()

	// read from client
	buf := make([]byte, 1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			fmt.Println(err)
		}
		
		message := string(buf[:n])
		fmt.Println("Received", message)

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
		fmt.Println("Error creating listene	r:", err)
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