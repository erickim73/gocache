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

// attempts transfer with automatic retires on failure
func TransferKeysWithRetry(targetAddr string, keys []string, values map[string]string, maxRetries int) error {
	var lastErr error

	// try up to maxRetries times
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("[TRANSFER] Attempt %d/%d to transfer %d keys to %s\n", attempt, maxRetries, len(keys), targetAddr)

		// attempt the transfer
		err := TransferKeys(targetAddr, keys, values)

		if err == nil {
			fmt.Printf("[TRANSFER] Transfer succeded on attempt %d\n", attempt)
			return nil
		}

		// failed - log and potentially retry
		lastErr = err
		fmt.Printf("[TRANSFER] Attempt %d failed: %v\n", attempt, err)

		// don't sleep after the last attempt
		if attempt < maxRetries {
			// exponential backoff
			backoff := time.Duration(attempt) * time.Second
			fmt.Printf("[TRANSFER] Retrying in %v...\n", backoff)
			time.Sleep(backoff)
		}
	}

	return fmt.Errorf("transfer failed after %d attempts: %v", maxRetries, lastErr)
}

// transfers keys in smaller batches to avoid overwhelming the network
func TransferKeysBatch(targetAddr string, keys []string, values map[string]string, batchSize int) error {
	// calculate how many batches we need
	totalKeys := len(keys)
	numBatches := (totalKeys + batchSize - 1) / batchSize // ceiling division

	fmt.Printf("[TRANSFER] Splitting %d keys into %d batches of %d\n", totalKeys, numBatches, batchSize)

	// process each batch
	for i := 0; i < numBatches; i++ {
		// calculate batch boundaries
		start := i * batchSize
		end := start + batchSize
		if end > totalKeys {
			end = totalKeys
		}

		// extract keys for this batch
		batchKeys := keys[start: end]

		// extract values for this batch
		batchValues := make(map[string]string)
		for _, key := range batchKeys {
			batchValues[key] = values[key]
		}

		fmt.Printf("[TRANSFER] Batch %d/%d: transferring keys %d-%d\n", i + 1, numBatches, start, end - 1)

		// transfer this batch with retries
		err := TransferKeysWithRetry(targetAddr, batchKeys, batchValues, 3)
		if err != nil {
			// if any batch fails, stop migration
			return fmt.Errorf("batch %d failed: %v", i + 1, err)
		}

		if i < numBatches - 1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	fmt.Printf("[TRANSFER] All %d batches completed successfully\n", numBatches)
	return nil
}

// checks if a response indicates success
func isOKResponse(response interface{}) bool {
	// response could be string or error
	switch v := response.(type) {
	case string:
		return v == "OK"
	case error:
		return false
	default:
		return false
	}
}

// checks that keys were successfully transferred
func VerifyTransfer(targetAddr string, keys []string) error {
	// connect to target
	conn, err := net.DialTimeout("tcp", targetAddr, 5 * time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect for verification: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	// check each key exists on target
	for _, key := range keys {
		// send GET command
		cmd := protocol.EncodeArray([]interface{}{"GET", key})
		_, err := conn.Write([]byte(cmd))
		if err != nil {
			return fmt.Errorf("verification failed for key %s: %v", key, err)
		}

		// read response
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		response, err := protocol.Parse(reader)
		if err != nil {
			return fmt.Errorf("verification read failed for key %s: %v", key, err)
		}

		// nil response means key doesn't exist on target
		if response == nil {
			return fmt.Errorf("key %s not found on target", key)
		}
	}

	fmt.Printf("[TRANSFER] Verification passed: all %d keys exist on target\n", len(keys))
	return nil
}