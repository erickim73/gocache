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

	resp := "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$7\r\ntesting\r\n"
	conn.Write([]byte(resp))

	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	fmt.Println("Server response: ", string(buf[:n]))

	resp2 := "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"
	conn.Write([]byte(resp2))

	buf2 := make([]byte, 1024)
	n, _ = conn.Read(buf2)
	fmt.Println("value for key: ", string(buf2[:n]))
}