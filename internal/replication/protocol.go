package replication

import (
	"bufio"
	"bytes"
	"errors"
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
	lastSeqNum := strconv.FormatInt(req.LastSeqNum, 10)
	command := protocol.EncodeArray([]interface{}{"SYNC", req.FollowerID, lastSeqNum})

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
		return nil, errors.New("expected length of array to be 3")
	}

	cmd, ok := arr[0].(string)
	if !ok || cmd != "SYNC" {
		return nil, errors.New("expected SYNC command")
	}

	followerID, ok := arr[1].(string)
	if !ok {
		return nil, errors.New("invalid follower ID")
	}

	lastSeqNumStr, ok := arr[2].(string)
	if !ok {
		return nil, errors.New("invalid sequence number")
	}

	lastSeqNum, err := strconv.ParseInt(lastSeqNumStr, 10, 64)
	if err != nil {
		return nil, err
	}

	return &SyncRequest{
		FollowerID: followerID,
		LastSeqNum: lastSeqNum,
	}, nil
}
