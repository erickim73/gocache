package main

import (
	"net"
	"log"
	"fmt"
)

func handleConnection(conn net.Conn) {
	defer conn.Close()
	// read from the client
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		log.Println(err)
	}

	fmt.Println(string(buf[:n]))

	fmt.Fprintf(conn, "Echo = " + string(buf[:n]))
}

func main() {
	ln, err := net.Listen("tcp", "localhost:8080")
	if err != nil {
		log.Fatalln(err)
	}

	defer ln.Close()

	for {
		// accept an incoming connection
		conn, err := ln.Accept()
		if err != nil {
			log.Println(err)
		}

		// handle the connection
		go handleConnection(conn)
	}
}