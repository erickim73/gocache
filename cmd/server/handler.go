package main

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/pubsub"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/internal/server"
	"github.com/erickim73/gocache/pkg/protocol"
)

// tracks whether a connection is in subscriber mode
type ConnectionState struct {
	subscriberMode bool // is connection subscribed to any channels

	// transaction state tracking
	inTransaction bool // true when client has called MULTI but not yet EXEC/DISCARD
	commandQueue [][]interface{} // queued commands waiting for EXEC (stores raw command slices)
	transactionError error // set if command validation failed during queueing
}

// returns true if the operation must be handled by the leader
func requiresLeader(operation string) bool {
	switch operation {
	case "SET", "DEL":
		return true // write operations need leader
	case "GET":
		return false // reads can be served by followers
	default:
		return true // unknown operations go to leader for safety
	}
}

// validate if a command can be queued in a transaction
func canQueueCommand(command string) bool {
	// transaction control commands can't be nested
	if command == "MULTI" || command == "EXEC" || command == "DISCARD" {
		return false
	}

	// pub/sub commands can't be used in transactions
	if command == "SUBSCRIBE" || command == "UNSUBSCRIBE" || command == "PUBLISH" || command == "PSUBSCRIBE" || command == "PUNSUBSCRIBE" {
		return false
	}

	// cluster commands shouldn't be in transactions
	if command == "CLUSTER" {
		return false
	}

	return true
}

// handle client commands and write to aof
func handleConnection(conn net.Conn, cache *cache.Cache, aof *persistence.AOF, nodeState *server.NodeState, ps *pubsub.PubSub) {
	defer conn.Close()

	// when connection closes, remove from all pub/sub channels
	defer ps.RemoveConnection(conn)

	// track connections
	cache.GetMetrics().IncrementActiveConnections()
	defer cache.GetMetrics().DecrementActiveConnections()

	// log new connection
	remoteAddr := conn.RemoteAddr().String()

	// track connection state
	state := &ConnectionState{
		subscriberMode: false,
		inTransaction: false,
		commandQueue: nil,
		transactionError: nil,
	}

	// read from client
	reader := bufio.NewReader(conn)
	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			if err == io.EOF {
				return
			}
			slog.Warn("Connection error", "address", remoteAddr, "error", err)
			return
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			slog.Warn("Invalid command format", "address", remoteAddr)
			return
		}
		command := resultSlice[0]

		// start timing request here
		startTime := time.Now()

		// check if connection in subscriber mode
		if state.subscriberMode {
			allowedCommands := map[string]bool{
				"SUBSCRIBE":    true,
				"UNSUBSCRIBE":  true,
				"PSUBSCRIBE":   true, // pattern subscribe
				"PUNSUBSCRIBE": true, // pattern unsubscribe
				"PING":         true, // health check always allowed
				"QUIT":         true, // graceful disconnect always allowed
			}

			cmdStr, ok := command.(string)
			if !ok || !allowedCommands[cmdStr] {
				// reject non-pub/sub commands in subscriber mode
				conn.Write([]byte(protocol.EncodeError("ERR only (P)SUBSCRIBE / (P)UNSUBSCRIBE / PING / QUIT allowed in this context")))

				// record latency for rejected command
				duration := time.Since(startTime)
				cache.GetMetrics().RecordOperationDuration(duration.Seconds())
				continue
			}
		}

		// check if we're in transaction mode and should queue this command
		cmdStr, ok := command.(string)
		if ok && state.inTransaction {
			// transaction control commands are handled immediately, not queued
			if cmdStr == "EXEC" || cmdStr == "DISCARD" {
				// handled by switch statement
			} else if canQueueCommand(cmdStr) {
				// queue command for later execution during EXEC
				queueCommandForTransaction(conn, state, resultSlice)

				duration := time.Since(startTime)
				cache.GetMetrics().RecordOperationDuration(duration.Seconds())
				continue
			} else {
				// command can't be queued
				conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("ERR command '%s cannot be used in a transaction", cmdStr))))

				duration := time.Since(startTime)
				cache.GetMetrics().RecordOperationDuration(duration.Seconds())
				continue
			}
		}

		// handle pub/sub commands
		switch command {
		case "SUBSCRIBE":
			// subscribe enters subscriber mode
			handleSubscribe(conn, resultSlice, ps, state)
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
			continue

		case "UNSUBSCRIBE":
			// unsubscribe may exit subscriber mode if no channels remain
			handleUnsubscribe(conn, resultSlice, ps, state)
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
			continue

		case "PUBLISH":
			// publish can be used in normal mode. allows one connection to publish while others subscribe
			handlePublish(conn, resultSlice, ps)
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
			continue
		}

		// handle CLUSTER commands first
		if command == "CLUSTER" {
			handleClusterCommand(conn, resultSlice, cache, nodeState)
			// record latency for cluster commands
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
			continue // skip forward check
		}

		// extract key from command
		var key string
		if len(resultSlice) >= 2 {
			key = resultSlice[1].(string)
		}

		// determine if this is a write operation
		isWrite := (command == "SET" || command == "DEL")

		// check if we should handle this key or forward it
		if nodeState.IsClusterMode() && key != "" {
			shouldForward, targetNodeID, targetAddr := nodeState.ShouldForwardRequest(key, isWrite)
			if shouldForward {
				// key belongs to another node - return MOVED
				msg := fmt.Sprintf("-MOVED %s %s\r\n", targetNodeID, targetAddr)
				conn.Write([]byte(msg))
				slog.Debug("Routing redirect - key belongs to different node",
					"key", key,
					"target_node", targetNodeID,
					"target_addr", targetAddr,
					"action", "returning MOVED",
				)

				// record latency for forwarded requests
				duration := time.Since(startTime)
				cache.GetMetrics().RecordOperationDuration(duration.Seconds())
				continue
			}

			// key belongs to this node or we can handle reads, log and continue
			if isWrite {
				slog.Debug("Routing write locally",
					"key", key,
					"reason", "this node is the leader",
				)
			} else {
				slog.Debug("Routing read locally",
					"key", key,
					"reason", "replicated data available",
				)
			}
		}

		// handle commands
		switch command {
		case "SET":
			// handleSet(conn, resultSlice, cache, aof, leader)
			leader := nodeState.GetLeader()
			handleSet(conn, resultSlice, cache, aof, leader)
			// record latency after set completes
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "GET":
			handleGet(conn, resultSlice, cache)
			// record latency after get completes
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "DEL":
			// handleDelete(conn, resultSlice, cache, aof, leader)
			leader := nodeState.GetLeader()
			handleDelete(conn, resultSlice, cache, aof, leader)
			// record latency after delete completes
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "DBSIZE":
			handleDBSize(conn, cache)
			// record latency for dbsize
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "PING":
			handlePing(conn)
			// record latency for ping
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "CLUSTER":
			handleClusterCommand(conn, resultSlice, cache, nodeState)
			// record latency for cluster
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "MULTI":
			handleMulti(conn, state)
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "EXEC":
			leader := nodeState.GetLeader()
			handleExec(conn, state, cache, aof, leader)
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "DISCARD":
			handleDiscard(conn, state)
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
		default:
			conn.Write([]byte(protocol.EncodeError("Unknown command " + command.(string))))
			// record latency for unknown commands
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
		}
	}
}

// queue a command for later execution during EXEC
func queueCommandForTransaction(conn net.Conn, state *ConnectionState, resultSlice []interface{}) {
	// it transaction already has an error, don't queue more commands. just return QUEUED to match redis behavior
	if state.transactionError != nil {
		conn.Write([]byte(protocol.EncodeSimpleString("QUEUED")))
		return
	}

	// validate command syntax before queueing
	command := resultSlice[0].(string)

	// validate based on command type
	var validationErr error
	switch command {
	case "SET":
		if len(resultSlice) < 3 || len(resultSlice) > 4 {
			validationErr = fmt.Errorf("wrong number of arguments for 'set' command")
		}
		case "GET":
		if len(resultSlice) != 2 {
			validationErr = fmt.Errorf("wrong number of arguments for 'get' command")
		}
	case "DEL":
		if len(resultSlice) != 2 {
			validationErr = fmt.Errorf("wrong number of arguments for 'del' command")
		}
	case "DBSIZE":
		if len(resultSlice) != 1 {
			validationErr = fmt.Errorf("wrong number of arguments for 'dbsize' command")
		}
	case "PING":
		// PING can have 0 or 1 arguments
		if len(resultSlice) > 2 {
			validationErr = fmt.Errorf("wrong number of arguments for 'ping' command")
		}
	}

	// if validation failed, mark transaction as failed
	if validationErr != nil {
		state.transactionError = validationErr
		conn.Write([]byte(protocol.EncodeError(validationErr.Error())))
		return
	}

	// validation passed. add command to queue
	state.commandQueue = append(state.commandQueue, resultSlice)
	
	// send queued response to client
	conn.Write([]byte(protocol.EncodeSimpleString("QUEUED")))

	slog.Debug("Command queued for transaction",
		"command", command,
		"queue_length", len(state.commandQueue),
	)
}

// start a transaction with MULTI command
func handleMulti(conn net.Conn, state *ConnectionState) {
	// check if already in a transaction
	if state.inTransaction {
		conn.Write([]byte(protocol.EncodeError("ERR MULTI calls can not be nested")))
		slog.Debug("Rejected nested MULTI call")
		return
	}

	// initialize transaction state
	state.inTransaction = true
	state.commandQueue = make([][]interface{}, 0) // empty queue for new transaction
	state.transactionError = nil // no errors yet

	// send ok response
	conn.Write([]byte(protocol.EncodeSimpleString("OK")))

	slog.Debug("Transaction started (MULTI called)")
}

// execute all queued commands atomically with EXEC
func handleExec(conn net.Conn, state *ConnectionState, cache *cache.Cache, aof *persistence.AOF, leader *replication.Leader) {
	// check if we're actually in a transaction
	if !state.inTransaction {
		conn.Write([]byte(protocol.EncodeError("ERR EXEC without MULTI")))
		slog.Debug("EXEC called without MULTI")
		return
	}

	// always reset transaction state when done
	defer func() {
		state.inTransaction = false
		state.commandQueue = nil
		state.transactionError = nil
	}()

	// if any command has validation error during queueing, abort transaction
	if state.transactionError != nil {
		conn.Write([]byte(protocol.EncodeError("EXECABORT Transaction discarded because of previous errors.")))
		slog.Debug("Transaction aborted due to validation error", 
			"error", state.transactionError,
		)
		return
	}

	results := make([]interface{}, len(state.commandQueue))
	
	slog.Debug("Executing transaction",
		"command_count", len(state.commandQueue),
	)

	// execute each queued command and collect results
	for i, cmdSlice := range state.commandQueue {
		command := cmdSlice[0].(string)

		// execute command and capture the response string
		var response string
		switch command {
		case "SET":
			response = executeSetCommand(cmdSlice, cache, aof, leader)
		case "GET":
			response = executeGetCommand(cmdSlice, cache)
		case "DEL":
			response = executeDeleteCommand(cmdSlice, cache, aof, leader)
		case "DBSIZE":
			response = executeDBSizeCommand(cache)
		case "PING":
			response = executePingCommand()
		default:
			response = protocol.EncodeError(fmt.Sprintf("ERR unknown command '%s'", command))
		}

		// store raw resp response
		results[i] = response

		slog.Debug("Transaction command executed",
			"index", i,
			"command", command,
			"response_preview", response[:min(len(response), 50)],
		)
	}

	// build resp array response containing all command results
	arrayResponse := fmt.Sprintf("*%d\r\n", len(results))
	for _, result := range results {
		arrayResponse += result.(string)
	}

	conn.Write([]byte(arrayResponse))
	
	slog.Debug("Transaction completed successfully",
		"commands_executed", len(results),
	)
}

// cancel a transaction with discard
func handleDiscard(conn net.Conn, state *ConnectionState) {
	// check if we're in a transaction
	if !state.inTransaction {
		conn.Write([]byte(protocol.EncodeError("ERR DISCARD without MULTI")))
		slog.Debug("DISCARD called without MULTI")
		return
	}

	// clear transaction state
	queuedCount := len(state.commandQueue)
	state.inTransaction = false
	state.commandQueue = nil
	state.transactionError = nil

	// send ok response
	conn.Write([]byte(protocol.EncodeSimpleString("OK")))

	slog.Debug("Transaction discarded",
		"queued_commands_discarded", queuedCount,
	)
}

// execute set command and return RESP formatted response
func executeSetCommand(resultSlice []interface{}, cache *cache.Cache, aof *persistence.AOF, leader *replication.Leader) string {
	// validate arguments
	if len(resultSlice) < 3 || len(resultSlice) > 4 {
		return protocol.EncodeError("ERR wrong number of arguments for 'set' command")
	}

	key := resultSlice[1].(string)
	value := resultSlice[2].(string)

	ttl := time.Duration(0)

	// parse ttl if found
	if len(resultSlice) == 4 {
		seconds := resultSlice[3].(string)
		ttlSec, err := strconv.Atoi(seconds)
		if err != nil {
			return protocol.EncodeError("ERR value is not an integer or out of range")
		}
		ttl = time.Duration(ttlSec) * time.Second
	}

	// perform set operation
	cache.Set(key, value, ttl)
	
	ttlSeconds := int64(ttl.Seconds())

	// replicate to followers
	if leader != nil {
		err := leader.Replicate(replication.OpSet, key, value, ttlSeconds)
		if err != nil {
			slog.Error("Replication failed",
				"operation", "SET",
				"key", key,
				"error", err,
			)
		}
	}

	// write to aof for persistence
	ttlSecondsStr := strconv.FormatInt(ttlSeconds, 10)
	aofCommand := protocol.EncodeArray([]interface{}{"SET", key, value, ttlSecondsStr})
	err := aof.Append(aofCommand)
	if err != nil {
		slog.Error("AOF write failed", 
			"operation", "SET",
			"key", key,
			"error", err,
		)
	}

	return protocol.EncodeSimpleString("OK")
}

// execute GET command return RESP formatted response
func executeGetCommand(resultSlice []interface{}, cache *cache.Cache) string {
	if len(resultSlice) != 2 {
		return protocol.EncodeError("ERR wrong number of arguments for 'get' command")
	}

	key := resultSlice[1].(string)

	result, exists := cache.Get(key)

	if !exists {
		// return null bulk string for missing keys
		return protocol.EncodeBulkString("", true)
	}
	
	return protocol.EncodeBulkString(result, false)
}

// execute DEL command return resp formatted response
func executeDeleteCommand(resultSlice []interface{}, cache *cache.Cache, aof *persistence.AOF, leader *replication.Leader) string {
	if len(resultSlice) != 2 {
		return protocol.EncodeError("ERR wrong number of arguments for 'del' command")
	}

	key := resultSlice[1].(string)

	// delete from cache
	cache.Delete(key)

	// replicate to followers
	if leader != nil {
		err := leader.Replicate(replication.OpDelete, key, "", 0)
		if err != nil {
			slog.Error("Error replicating DEL command from leader to follower",
				"error", err,
				"key", key,
			)
		}
	}

	// write to AOF
	aofCommand := protocol.EncodeArray([]interface{}{"DEL", key})
	err := aof.Append(aofCommand)
	if err != nil {
		slog.Error("Failed to write to AOF",
			"error", err,
			"command", "DEL",
			"key", key,
		)
	}

	return protocol.EncodeSimpleString("OK")
}

// execute DBSIZE command and return RESP formatted response
func executeDBSizeCommand(cache *cache.Cache) string {
	keys := cache.GetAllKeys()
	count := len(keys)
	return fmt.Sprintf(":%d\r\n", count)
}

// execute PING command and return RESP formmated response
func executePingCommand() string {
	return protocol.EncodeSimpleString("PONG")
}

// process subscribe commands
func handleSubscribe(conn net.Conn, resultSlice []interface{}, ps *pubsub.PubSub, state *ConnectionState) {
	// validate command format
	if len(resultSlice) < 2 {
		conn.Write([]byte(protocol.EncodeError("ERR wrong number of arguments for 'subscribe' command")))
		return
	}

	// subscribe to each channel specified in command
	for i := 1; i < len(resultSlice); i++ {
		channel, ok := resultSlice[i].(string)
		if !ok {
			conn.Write([]byte(protocol.EncodeError("ERR invalid channel")))
			return
		}

		// call PubSub.Subscribe to add this connection to the channel
		err := ps.Subscribe(conn, channel)
		if err != nil {
			slog.Error("Failed to subscribe",
				"channel", channel,
				"error", err,
			)
			conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("ERR subscribe failed: %v", err))))
			return
		}

		// get total number of channels this connection is subscribed to
		subscribedChannels := ps.GetSubscribedChannels(conn)
		totalSubscriptions := len(subscribedChannels)

		// send subscription confirmation in resp format
		confirmation := protocol.EncodeSubscribeConfirmation(channel, totalSubscriptions)
		conn.Write([]byte(confirmation))

		slog.Debug("Client subscribed to channel",
			"channel", channel,
			"total_subscriptions", totalSubscriptions,
			"remote_addr", conn.RemoteAddr(),
		)
	}

	// enter subscriber mode
	state.subscriberMode = true
}

// process unsubscribe commands
func handleUnsubscribe(conn net.Conn, resultSlice []interface{}, ps *pubsub.PubSub, state *ConnectionState) {
	// case 1: unsubscribe with no arguments: unsubscribe from all channels
	if len(resultSlice) == 1 {
		channels := ps.GetSubscribedChannels(conn)

		// unsubscribe from each channel the connection is subscribed to
		for _, channel := range channels {
			err := ps.Unsubscribe(conn, channel)
			if err != nil {
				slog.Error("Failed to unsubscribe",
					"channel", channel,
					"error", err,
				)
			}

			// get remaining subscription count
			remaining := len(ps.GetSubscribedChannels(conn))

			// send confirmation for this channel
			confirmation := protocol.EncodeUnsubscribeConfirmation(channel, remaining)
			conn.Write([]byte(confirmation))

			slog.Debug("Client unsubscribed from channel",
				"channel", channel,
				"remaining_subscriptions", remaining,
				"remote_addr", conn.RemoteAddr(),
			)
		}

		// exit subscriber mode since we're no longer subscribed to anything
		state.subscriberMode = false
		return
	}

	// case 2: unsubscribe from specific channels
	for i := 1; i < len(resultSlice); i++ {
		channel, ok := resultSlice[i].(string)
		if !ok {
			conn.Write([]byte(protocol.EncodeError("ERR invalid channel name")))
			return
		}

		// unsubscribe from specific channel
		err := ps.Unsubscribe(conn, channel)
		if err != nil {
			slog.Debug("Unsubscribe attempted on non-subscribed channel",
				"channel", channel,
				"error", err,
			)
		}

		// get remaining subscription count
		remaining := len(ps.GetSubscribedChannels(conn))

		// send confirmation
		confirmation := protocol.EncodeUnsubscribeConfirmation(channel, remaining)
		conn.Write([]byte(confirmation))

		slog.Debug("Client unsubscribed from channel",
			"channel", channel,
			"remaining_subscriptions", remaining,
			"remote_addr", conn.RemoteAddr(),
		)
	}

	// check if we should exit subscriber mode
	if len(ps.GetSubscribedChannels(conn)) == 0 {
		state.subscriberMode = false
	}
}

// process publish commands
func handlePublish(conn net.Conn, resultSlice []interface{}, ps *pubsub.PubSub) {
	// valid command format
	if len(resultSlice) != 3 {
		conn.Write([]byte(protocol.EncodeError("ERR wrong number of arguments for 'publish' command")))
		return
	}

	// extract channel name
	channel, ok := resultSlice[1].(string)
	if !ok {
		conn.Write([]byte(protocol.EncodeError("ERR invalid channel name")))
		return
	}

	// extract message content
	message, ok := resultSlice[2].(string)
	if !ok {
		conn.Write([]byte(protocol.EncodeError("ERR invalid message")))
		return
	}

	// publish message to all subscribers of this channel
	count := ps.Publish(channel, message)

	// send back number of subscribers as an integer
	response := protocol.EncodePublishResponse(count)
	conn.Write([]byte(response))

	slog.Debug("Message published",
		"channel", channel,
		"subscribers", count,
		"message_length", len(message),
	)
}

// process set commands
func handleSet(conn net.Conn, resultSlice []interface{}, cache *cache.Cache, aof *persistence.AOF, leader *replication.Leader) {
	if len(resultSlice) < 3 || len(resultSlice) > 4 {
		conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
		return
	}

	key := resultSlice[1].(string)
	value := resultSlice[2].(string)

	ttl := time.Duration(0)

	// if ttl provided as a 4th argument
	if len(resultSlice) == 4 {
		seconds := resultSlice[3].(string)
		ttlSec, err := strconv.Atoi(seconds)
		if err != nil {
			conn.Write([]byte(protocol.EncodeError("Couldn't convert seconds to a string")))
			return
		}
		ttl = time.Duration(ttlSec) * time.Second
	}

	cache.Set(key, value, ttl)

	ttlSeconds := int64(ttl.Seconds()) // 0 if no TTL

	// only replicate if we have a leader (non-cluster mode)
	if leader != nil {
		err := leader.Replicate(replication.OpSet, key, value, ttlSeconds)
		if err != nil {
			slog.Error("Replication failed",
				"operation", "SET",
				"key", key,
				"error", err,
			)
		}
	}

	// write to aof
	ttlSecondsStr := strconv.FormatInt(ttlSeconds, 10)
	aofCommand := protocol.EncodeArray([]interface{}{"SET", key, value, ttlSecondsStr})
	err := aof.Append(aofCommand)
	if err != nil {
		slog.Error("AOF write failed",
			"operation", "SET",
			"key", key,
			"error", err,
		)
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

func handleGet(conn net.Conn, resultSlice []interface{}, cache *cache.Cache) {
	if len(resultSlice) != 2 {
		conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
		return
	}

	key := resultSlice[1].(string)

	result, exists := cache.Get(key)

	if !exists {
		conn.Write([]byte(protocol.EncodeBulkString("", true)))
	} else {
		conn.Write([]byte(protocol.EncodeBulkString(result, false)))
	}
}

func handleDelete(conn net.Conn, resultSlice []interface{}, cache *cache.Cache, aof *persistence.AOF, leader *replication.Leader) {
	if len(resultSlice) != 2 {
		conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
		return
	}

	key := resultSlice[1].(string)

	cache.Delete(key)

	// send to followers
	if leader != nil {
		err := leader.Replicate(replication.OpDelete, key, "", 0)
		if err != nil {
			slog.Error("Error replicating DEL command from leader to follower",
				"error", err,
				"key", key,
			)
		}
	}

	aofCommand := protocol.EncodeArray([]interface{}{"DEL", key})
	err := aof.Append(aofCommand)
	if err != nil {
		slog.Error("Failed to write to AOF",
			"error", err,
			"command", "DEL",
			"key", key,
		)
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// returns the number of keys in the cache
func handleDBSize(conn net.Conn, cache *cache.Cache) {
	// get all keys
	keys := cache.GetAllKeys()
	count := len(keys)

	// encode as integer
	response := fmt.Sprintf(":%d\r\n", count)
	conn.Write([]byte(response))
}

// responds to PING with PONG
func handlePing(conn net.Conn) {
	conn.Write([]byte(protocol.EncodeSimpleString("PONG")))
}
