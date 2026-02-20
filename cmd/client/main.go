package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/erickim73/gocache/pkg/client"
)

func main() {
	// create a tcp socket
	conn, err := client.NewClient("localhost:6379")
	if err != nil {
		log.Fatalln(err)
	}

	defer conn.Close()

	fmt.Println("Connected to server.")
	fmt.Println("Commands: SET key value [ttl], GET key, DEL key")

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("You:")
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(text)

		// parse command into parts
		parts := strings.Fields(text)
		if len(parts) == 0 {
			continue
		}

		cmd := strings.ToUpper(parts[0]) // make command case-insensitive

		switch cmd {
		case "SET":
			if len(parts) < 3 {
				fmt.Println("Usage: SET key value [ttl]")
				continue
			}
			key := parts[1]
			value := parts[2]

			if len(parts) == 4 {
				// set with ttl
				var ttl int
				fmt.Sscanf(parts[3], "%d", &ttl)
				err = conn.SetWithTTL(key, value, ttl)
			} else {
				// set without ttl
				err = conn.Set(key, value)
			}

			if err != nil {
				fmt.Println("Error:", err)
			} else {
				fmt.Println("OK")
			}
		case "GET":
			if len(parts) != 2 {
				fmt.Println("Usage: GET key")
				continue
			}
			value, err := conn.Get(parts[1])
			if err != nil {
				fmt.Println("Error:", err)
			} else {
				fmt.Println("Value:", value)
			}
		case "DEL", "DELETE":
			if len(parts) != 2 {
				fmt.Println("Usage: DEL key")
				continue
			}
			err = conn.Delete(parts[1])
			if err != nil {
				fmt.Println("Error:", err)
			} else {
				fmt.Println("OK")
			}
		case "QUIT", "EXIT": // allow graceful exit
			fmt.Println("Goodbye!")
			return

		default:
			fmt.Println("Unknown command. Available SET, GET, DEL, QUIT")
		}
	}
}