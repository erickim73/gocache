package client

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/erickim73/gocache/pkg/protocol"
)

// number of redirects to follow
const MaxRedirects = 5

// client struct to hold connection state
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	mu sync.Mutex
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
	c.mu.Lock()
	defer c.mu.Unlock()
	
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

// read response from server
func (c *Client) readResponse() (string, error) {
	// read resp responses
	result, err := protocol.Parse(c.reader)
	if err != nil {
		return "", err
	}

	// convert parsed result back to string format
	switch v := result.(type) {
	case string:
		return v, nil
	case int64:
		return fmt.Sprintf(":%d\r\n", v), nil
	case []byte:
		return string(v), nil
	case []interface{}:
		return fmt.Sprintf("%v", v), nil
	default:
		return fmt.Sprintf("%v", result), nil
	}
}

// high level set command
func (c *Client) Set(key string, value string) error {
	command := []interface{}{"SET", key, value}
	response, err := c.executeCommandWithRedirect(command)
	if err != nil {
		return err
	}

	// check if response is OK
	if response != "OK" {
		return fmt.Errorf("unexpected response: %s", response)
	}

	return nil
}

// high level set command with ttl
func (c *Client) SetWithTTL(key string, value string, ttl int) error {
	command := []interface{}{"SET", key, value, fmt.Sprintf("%d", ttl)}
	response, err := c.executeCommandWithRedirect(command)
	if err != nil {
		return err
	}

	if response != "OK" {
		return fmt.Errorf("unexpected response: %s", response)
	}

	return nil
}

// high level get command
func (c *Client) Get(key string) (string, error) {
	command := []interface{}{"GET", key}
	response, err := c.executeCommandWithRedirect(command)
	if err != nil {
		return "", err
	}

	// parse bulk string response
	return response, nil
}

// high level delete command
func (c *Client) Delete(key string) error {
	command := []interface{}{"DEL", key}
	response, err := c.executeCommandWithRedirect(command)
	if err != nil {
		return err
	}

	if response != "OK" && response != ":1\r\n" && response != "1" {
		return fmt.Errorf("unexpected response: %s", response)
	}

	return nil
}

// sends a generic command to the server and returns the response
func (c *Client) SendCommand(args ...string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// build RESP array with command arguments
	interfaceArgs := make([]interface{}, len(args))
	for i, arg := range args {
		interfaceArgs[i] = arg
	}

	// encode and send the command
	cmd := protocol.EncodeArray(interfaceArgs)

	_, err := c.writer.WriteString(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to write command: %v", err)
	}

	// flush the writer to ensure data is sent
	err = c.writer.Flush()
	if err != nil {
		return "", fmt.Errorf("failed to flush: %v", err)
	}

	// read the response
	response, err := protocol.Parse(c.reader)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %v", err) 
	}

	// handle different response types
	switch v := response.(type) {
	case string: 
		return v, nil
	case error:
		return "", v
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func main() {
	// create a tcp socket
	conn, err := NewClient("localhost:6379")
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
