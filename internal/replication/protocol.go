package replication

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/erickim73/gocache/pkg/protocol"
)

// operation type constants
const (
	OpSet = "SET"
	OpDelete = "DELETE"
)

// command type constants
const (
	CmdSync = "SYNC"
	CmdReplicate = "REPLICATE"
	CmdHeartbeat = "HEARTBEAT"
)

type SyncRequest struct {
	FollowerID string //unique id of follower
	LastSeqNum int64 // last sequence number received (for reconnection)
}

type ReplicateCommand struct {
	SeqNum int64
	Operation string
	Key string
	Value string
	TTL int64
}

type HeartbeatCommand struct {
	SeqNum int64
	NodeID string
}

func EncodeSyncRequest(req *SyncRequest) ([]byte, error) {
	if req == nil {
		return nil, errors.New("request cannot be nil")
	}
	
	command := protocol.EncodeArray([]interface{}{
		CmdSync, 
		req.FollowerID, 
		req.LastSeqNum,
	})

	return []byte(command), nil
}
func DecodeSyncRequest(data []byte) (*SyncRequest, error) {
	reader := bufio.NewReader(bytes.NewReader(data))
	
	value, err := protocol.Parse(reader)
	if err != nil {
		return nil, err
	}

	arr, ok := value.([]interface{})
	if !ok {
		return nil, errors.New("expected array")
	}

	if len(arr) != 3 {
		return nil, fmt.Errorf("expected length of array to be 3, got %d", len(arr))
	}

	cmd, ok := arr[0].(string)
	if !ok || cmd != CmdSync {
		return nil, errors.New("expected SYNC command")
	}

	followerID, ok := arr[1].(string)
	if !ok {
		return nil, errors.New("invalid follower ID")
	}

	// handle both string and int types for LastSeqNum
	var lastSeqNum int64
	switch v := arr[2].(type) {
	case int:
		lastSeqNum = int64(v)
	case string:
		lastSeqNum, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid sequence number: %w", err)
		}
	default:
		return nil, fmt.Errorf("sequence number must be int or string, got %s", v)
	}

	return &SyncRequest{
		FollowerID: followerID,
		LastSeqNum: lastSeqNum,
	}, nil
}

func EncodeReplicateCommand(comm *ReplicateCommand) ([]byte, error) {
	if comm == nil {
		return nil, errors.New("command cannot be nil")
	}
	
	if comm.Operation == OpSet {
		command := protocol.EncodeArray([]interface{}{
			CmdReplicate,
			comm.SeqNum,
			comm.Operation,
			comm.Key,
			comm.Value,
			comm.TTL,
		})
		return []byte(command), nil
	} else if comm.Operation == OpDelete {
		command := protocol.EncodeArray([]interface{}{
			CmdReplicate,
			comm.SeqNum,
			comm.Operation,
			comm.Key,
		})
		return []byte(command), nil
	}
	
	return nil, fmt.Errorf("invalid operation: %s", comm.Operation)
}

func DecodeReplicateCommand(data []byte) (*ReplicateCommand, error) {
	reader := bufio.NewReader(bytes.NewReader(data))

	parsed, err := protocol.Parse(reader)
	if err != nil {
		return nil, err
	}

	arr, ok := parsed.([]interface{})
	if !ok {
		return nil, errors.New("expected array")
	}

	cmd, ok := arr[0].(string)
	if !ok || cmd != CmdReplicate {
		return nil, errors.New("expected REPLICATE command")
	}

	// handle both string and int types for seqNum
	var seqNum int64
	switch v := arr[1].(type) {
	case int:
		seqNum = int64(v)
	case string:
		seqNum, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid sequence number: %w", err)
		}
	default:
		return nil, fmt.Errorf("sequence number must be int or string, got %s", v)
	}

	operation, ok := arr[2].(string)
	if !ok || (operation != OpSet && operation != OpDelete) {
		return nil, errors.New("expected operation to be SET or DELETE")
	}

	key, ok := arr[3].(string)
	if !ok {
		return nil, errors.New("expected key to be a string")
	}

	var value string
	var ttl int64
	if operation == OpSet {
		if len(arr) != 6 {
			return nil, fmt.Errorf("SET operation requires 6 elements, got %d", len(arr))
		}
		
		value, ok = arr[4].(string)
		if !ok {
			return nil, errors.New("expected value to be a string")
		}

		// handle both int and int64
		switch v := arr[5].(type) {
		case int:
			ttl = int64(v)
		case int64:
			ttl = v
		default:
			return nil, errors.New("expected ttl to be an integer")
		}

	} else if operation == OpDelete {
		if len(arr) != 4 {
			return nil, fmt.Errorf("DELETE operation requires 4 elements, got %d", len(arr))
		}
		value = ""
		ttl = 0
	}

	return &ReplicateCommand{
		SeqNum: seqNum,
		Operation: operation,
		Key: key,
		Value: value,
		TTL: ttl,
	}, nil
}

func EncodeHeartbeatCommand(req *HeartbeatCommand) ([]byte, error) {
	if req == nil {
		return nil, errors.New("command cannot be nil")
	}
	
	command := protocol.EncodeArray([]interface{}{
		CmdHeartbeat,
		req.SeqNum,
		req.NodeID,
	})

	return []byte(command), nil
}

func DecodeHeartbeatCommand(data []byte) (*HeartbeatCommand, error) {
	reader := bufio.NewReader(bytes.NewReader(data))

	value, err := protocol.Parse(reader)
	if err != nil {
		return nil, err
	}

	arr, ok := value.([]interface{})
	if !ok {
		return nil, errors.New("expected array")
	}

	if len(arr) != 3 {
		return nil, fmt.Errorf("expected length of array to be 3, got %d", len(arr))
	}

	cmd, ok := arr[0].(string)
	if !ok || cmd != CmdHeartbeat {
		return nil, errors.New("expected HEARTBEAT command")
	}

	// handle both string and int types for seqNum
	var seqNum int64
	switch v := arr[1].(type) {
	case int:
		seqNum = int64(v)
	case string:
		seqNum, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid sequence number: %w", err)
		}
	default:
		return nil, fmt.Errorf("sequence number must be int or string, got %s", v)
	}

	nodeID, ok := arr[2].(string)
	if !ok {
		return nil, errors.New("expected nodeID to be a string")
	}

	return &HeartbeatCommand{
		SeqNum: seqNum,
		NodeID: nodeID,
	}, nil
}