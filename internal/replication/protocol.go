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
