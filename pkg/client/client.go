package main

import (
	"net"
	"log"
	"fmt"
	"os"
	"bufio"
)

func main() {
	// create a tcp socket
	conn, err := net.Dial("tcp", "localhost:6379")
	if err != nil {
		log.Fatalln(err)
	}

	defer conn.Close()

	fmt.Println("Connected to server.")

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("You:")
		text, _ := reader.ReadString('\n')

		// send message to server
		_, err := conn.Write([]byte(text))
		if err != nil {
			fmt.Println("Error sending message to server:", err)
			return
		}

		// read reply
		reply := make([]byte, 1024)
		n, err := conn.Read(reply)
		if err != nil {
			fmt.Println("Error reading reply:", err)
			return
		}

		fmt.Println("Server:", string(reply[:n]))
	}
}