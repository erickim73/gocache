package replication

import (
	"bufio"
	"bytes"
	"strconv"
	"time"

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
	TTL time.Time
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
	
	arr, err := protocol.Parse(reader)
	if err != nil {
		return nil, err
	}

	return arr, nil
}
