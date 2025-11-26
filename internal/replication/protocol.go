package replication

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"

	"github.com/erickim73/gocache/pkg/protocol"
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

type HeartBeat struct {
	SeqNum int64
	NodeID string
}

func EncodeSyncRequest(req *SyncRequest) []byte {
	lastSeqNum := strconv.FormatInt(req.LastSeqNum, 10)
	command := protocol.EncodeArray([]string{"SYNC", req.FollowerID, lastSeqNum})

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

	return arr, nil
}
