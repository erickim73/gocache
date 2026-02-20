package cluster

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/erickim73/gocache/pkg/protocol"
)

// sends a batch of key-value pairs to another node
func TransferKeys(targetAddr string, keys []string, values map[string]string) error {
	// establish tcp connection to target node
	conn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
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
			slog.Warn("Failed to send key during transfer",
				"key", key,
				"target", targetAddr,
				"error", err,
			)
			continue
		}

		// set read deadline to prevent hanging on response
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))

		// read response from target node
		response, err := protocol.Parse(reader)
		if err != nil {
			failCount++
			slog.Warn("Failed to read response during transfer",
				"key", key,
				"target", targetAddr,
				"error", err,
			)
			continue
		}

		// verify the response is "OK"
		if !isOKResponse(response) {
			failCount++
			slog.Warn("Target rejected key",
				"key", key,
				"target", targetAddr,
				"response", response,
			)
			continue
		}

		successCount++
	}

	// log final statistics
	slog.Info("Transfer batch completed",
		"target", targetAddr,
		"succeeded", successCount,
		"failed", failCount,
		"total", len(keys),
	)

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
		slog.Info("Transfer attempt starting",
			"attempt", attempt,
			"max_retries", maxRetries,
			"key_count", len(keys),
			"target", targetAddr,
		)

		// attempt the transfer
		err := TransferKeys(targetAddr, keys, values)

		if err == nil {
			slog.Info("Transfer succeeded",
				"attempt", attempt,
				"target", targetAddr,
			)
			return nil
		}

		// failed - log and potentially retry
		lastErr = err
		slog.Warn("Transfer attempt failed",
			"attempt", attempt,
			"max_retries", maxRetries,
			"target", targetAddr,
			"error", err,
		)

		// don't sleep after the last attempt
		if attempt < maxRetries {
			// exponential backoff
			backoff := time.Duration(attempt) * time.Second
			slog.Debug("Retrying after backoff",
				"backoff_seconds", backoff.Seconds(),
				"next_attempt", attempt+1,
			)
			time.Sleep(backoff)
		}
	}

	slog.Error("Transfer failed after all retries",
		"max_retries", maxRetries,
		"target", targetAddr,
		"error", lastErr,
	)

	return fmt.Errorf("transfer failed after %d attempts: %v", maxRetries, lastErr)
}

// transfers keys in smaller batches to avoid overwhelming the network
func TransferKeysBatch(targetAddr string, keys []string, values map[string]string, batchSize int) error {
	// calculate how many batches we need
	totalKeys := len(keys)
	numBatches := (totalKeys + batchSize - 1) / batchSize // ceiling division

	slog.Info("Starting batched transfer",
		"target", targetAddr,
		"total_keys", totalKeys,
		"batch_count", numBatches,
		"batch_size", batchSize,
	)

	// create connection once for all batches
	conn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		slog.Error("Failed to connect for batch transfer",
			"target", targetAddr,
			"error", err,
		)

		return fmt.Errorf("failed to connect to %s: %v", targetAddr, err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	// process each batch
	for i := 0; i < numBatches; i++ {
		// calculate batch boundaries
		start := i * batchSize
		end := start + batchSize
		if end > totalKeys {
			end = totalKeys
		}

		// extract keys for this batch
		batchKeys := keys[start:end]

		// extract values for this batch
		batchValues := make(map[string]string)
		for _, key := range batchKeys {
			batchValues[key] = values[key]
		}

		slog.Debug("Processing batch",
			"batch_number", i+1,
			"total_batches", numBatches,
			"key_range", fmt.Sprintf("%d-%d", start, end-1),
			"batch_size", len(batchKeys),
		)

		// transfer this batch with retries
		err := transferKeysOnConnection(conn, reader, batchKeys, batchValues)
		if err != nil {
			// if any batch fails, stop migration
			slog.Error("Batch transfer failed",
				"batch_number", i+1,
				"total_batches", numBatches,
				"error", err,
			)

			return fmt.Errorf("batch %d failed: %v", i+1, err)
		}

		if i < numBatches-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	slog.Info("All batches transferred successfully",
		"target", targetAddr,
		"batch_count", numBatches,
		"total_keys", totalKeys,
	)

	return nil
}

// transfers keys by using existing connection
func transferKeysOnConnection(conn net.Conn, reader *bufio.Reader, keys []string, values map[string]string) error {
	successCount := 0
	failCount := 0

	for _, key := range keys {
		value := values[key]
		cmd := protocol.EncodeArray([]interface{}{"SET", key, value})

		_, err := conn.Write([]byte(cmd))
		if err != nil {
			failCount++
			slog.Warn("Failed to send key on existing connection",
				"key", key,
				"error", err,
			)
			continue
		}

		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		response, err := protocol.Parse(reader)
		if err != nil {
			failCount++
			slog.Warn("Failed to read response on existing connection",
				"key", key,
				"error", err,
			)
			continue
		}

		if !isOKResponse(response) {
			failCount++
			slog.Warn("Key rejected by target",
				"key", key,
				"response", response,
			)
			continue
		}

		successCount++
	}

	slog.Debug("Connection batch completed",
		"succeeded", successCount,
		"failed", failCount,
		"total", len(keys),
	)

	if failCount > 0 {
		return fmt.Errorf("transfer incomplete: %d keys failed", failCount)
	}

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
	slog.Debug("Starting transfer verification",
		"target", targetAddr,
		"key_count", len(keys),
	)

	// connect to target
	conn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		slog.Error("Failed to connect for verification",
			"target", targetAddr,
			"error", err,
		)

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
			slog.Error("Verification write failed",
				"key", key,
				"target", targetAddr,
				"error", err,
			)

			return fmt.Errorf("verification failed for key %s: %v", key, err)
		}

		// read response
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		response, err := protocol.Parse(reader)
		if err != nil {
			slog.Error("Verification read failed",
				"key", key,
				"target", targetAddr,
				"error", err,
			)

			return fmt.Errorf("verification read failed for key %s: %v", key, err)
		}

		// nil response means key doesn't exist on target
		if response == nil {
			slog.Error("Key not found on target during verification",
				"key", key,
				"target", targetAddr,
			)

			return fmt.Errorf("key %s not found on target", key)
		}
	}

	slog.Info("Transfer verification passed",
		"target", targetAddr,
		"verified_keys", len(keys),
	)
	return nil
}
