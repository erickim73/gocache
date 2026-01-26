package main

import (
	"net"
	"log"
	"fmt"
	"os"
	"bufio"

	"github.com/erickim73/gocache/pkg/protocol"
)

// number of redirects to follow
const MaxRedirects = 5

// client struct to hold connection state
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

// creates a new client connection
func NewClient(address string) (*Client, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %v", err)
	}

	return &Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
	}, nil
}

// close the client connection
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// send a command and handle redirects automatically
func (c *Client) executeCommandWithRedirect(command []interface{}) (string, error) {
	redirectCount := 0

	for redirectCount < MaxRedirects {
		// encode command using protocol package
		encoded := protocol.EncodeArray(command)

		// send command
		_, err := c.writer.WriteString(encoded)
		if err != nil {
			return "", fmt.Errorf("failed to write command: %v", err)
		}

		err = c.writer.Flush()
		if err != nil {
			return "", fmt.Errorf("failed to flush: %v", err)
		}

		// read response
		response, err := c.readResponse()
		if err != nil {
			return "", fmt.Errorf("failed to read response: %v", err)
		}

		// check if response is redirect
		if protocol.IsRedirect(response) {
			// parse redirect
			redirect, err := protocol.ParseRedirect(response)
			if err != nil {
				return "", fmt.Errorf("failed to parse redirect: %v", err)
			}

			// close current connection
			c.Close()

			// connect to leader
			fmt.Printf("Redirecting to leader at %s...\n", redirect.Address())
			newConn, err := net.Dial("tcp", redirect.Address())
			if err != nil {
				return "", fmt.Errorf("failed to connect to leader: %v", err)
			}

			// update client's connection
			c.conn = newConn
			c.reader = bufio.NewReader(newConn)
			c.writer = bufio.NewWriter(newConn)

			redirectCount++
			continue // retry with new connection
		}

		// not a redirect, return response
		return response, nil
	}

	return "", fmt.Errorf("too many redirects (%d)", MaxRedirects)
}

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