package cluster

import (
	"bufio"
	"fmt"
	"net"
	"time"

	"github.com/erickim73/gocache/pkg/protocol"
)

// sends a batch of key-value pairs to another node
func TransferKeys(targetAddr string, keys []string, values map[string]string) error {
	// establish tcp connection to target node
	conn, err := net.DialTimeout("tcp", targetAddr, 5 * time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %v", targetAddr, err)
	}

	defer conn.Close()

	reader := bufio.NewReader(conn)

	// track success/failure
	successCount := 0
	failCount := 0

	for _, key := range keys {
		value := values[key]

		// send set command in resp protocol format
		cmd := protocol.EncodeArray([]interface{}{"SET", key, value})

		// write command to network connection
		_, err := conn.Write([]byte(cmd))
		if err != nil {
			failCount++
			// log error but continue with other keys
			fmt.Printf("[TRANSFER] Failed to send key %s: %v\n", key, err)
			continue
		}

		// set read deadline to prevent hanging on response
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))

		// read response from target node
		response, err := protocol.Parse(reader)
		if err != nil {
			failCount++
			fmt.Printf("[TRANSFER] Failed to read response for key %s: %v\n", key, err)
			continue
		}

		// verify the response is "OK"
		if !isOKResponse(response) {
			failCount++
			fmt.Printf("[TRANSFER] Target rejected key %s: %v\n", key, response)
			continue
		}

		successCount++
	}

	// log final statistics
	fmt.Printf("[TRANSFER] Completed: %d succeeded, %d failed out of %d keys\n", successCount, failCount, len(keys))

	// fail the entire transfer if any keys failed. this prevents data loss
	if failCount > 0 {
		return fmt.Errorf("transfer incomplete: %d keys failed", failCount)
	}

	return nil
}