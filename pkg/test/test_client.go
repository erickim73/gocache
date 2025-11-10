package main

import (
	"net"
	"fmt"
)

func main() {  // Changed from test_client
	conn, err := net.Dial("tcp", "localhost:6379")
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		return
	}
	defer conn.Close()

	resp := "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"
	conn.Write([]byte(resp))

	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	fmt.Println("Server response: ", string(buf[:n]))
}