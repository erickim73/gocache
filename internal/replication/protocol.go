package replication

import (
	"time"
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

