package main

import (
	"net"
	"log"
	"fmt"
	"io"
	"strings"
)

func main() {
	// create a tcp socket
	conn, err := net.Dial("tcp", "localhost:6379")
	if err != nil {
		log.Fatalln(err)
	}

	defer conn.Close()

	// send a message
	data := "Hello, server!"
	if _, err := io.Copy(conn, strings.NewReader(data)); err != nil {
		fmt.Println("Error sending data:", err)
		return
	}
	
	buf := make([]byte, len(data))
	_, err = io.ReadFull(conn, buf)
	if err != nil {
		fmt.Println("Error reading data:", err)
		return
	}

	fmt.Println(string(buf))
}