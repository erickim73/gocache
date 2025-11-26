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

type Heartbeat struct {
	SeqNum int64
	NodeID string
}

func EncodeSyncRequest(req *SyncRequest) []byte {
	command := protocol.EncodeArray([]interface{}{
		CmdSync, 
		req.FollowerID, 
		req.LastSeqNum,
	})

	return []byte(command)
}
func DecodeSyncRequest(data []byte) (interface{}, error) {
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
		lastSeqNum = arr[2].(int64)
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

func EncodeReplicateCommand(comm *ReplicateCommand) []byte {
	command := protocol.EncodeArray([]interface{}{
		CmdReplicate,
		comm.SeqNum,
		comm.Operation,
		comm.Key,
		comm.Value,
		comm.TTL,
	})

	return []byte(command)
}

func DecodeReplicateCommand(data []byte) (interface{}, error) {
	reader := bufio.NewReader(bytes.NewReader(data))

	parsed, err := protocol.Parse(reader)
	if err != nil {
		return nil, err
	}

	arr, ok := parsed.([]interface{})
	if !ok {
		return nil, errors.New("expected array")
	}

	if len(arr) != 5 || len(arr) != 6 {
		return nil, fmt.Errorf("expected length of array to be 6, got %d", len(arr))
	}

	cmd, ok := arr[0].(string)
	if !ok || cmd != CmdReplicate {
		return nil, errors.New("expected REPLICATE command")
	}

	// handle both string and int types for seqNum
	var seqNum int64
	switch v := arr[1].(type) {
	case int:
		seqNum = arr[1].(int64)
	case string:
		seqNum, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid sequence number: %w", err)
		}
	default:
		return nil, fmt.Errorf("sequence number must be int or string, got %s", v)
	}

	operation, ok := arr[2].(string)
	if !ok || operation != OpSet || operation != OpDelete {
		return nil, errors.New("expected operation to be SET or DELETE")
	}

	key, ok := arr[3].(string)
	if !ok {
		return nil, errors.New("expected key to be a string")
	}

	var value string
	var ttl int64
	if operation == OpSet {
		value, ok = arr[4].(string)
		if !ok {
			return nil, errors.New("expected value to be a string")
		}

		ttl, ok = arr[5].(int64)
		if !ok {
			return nil, errors.New("expected ttl to be of type int64")
		}
	} else if operation == OpDelete {
		ttl, ok = arr[4].(int64)
		if !ok {
			return nil, errors.New("expected ttl to be of type int64")
		}
	}

	return &ReplicateCommand{
		SeqNum: seqNum,
		Operation: operation,
		Key: key,
		Value: value,
		TTL: ttl,
	}, nil


}