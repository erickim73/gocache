package replication

type SyncRequest struct {
	FollowerID string //unique id of follower
	LastSeqNum int64 // last sequence number received (for reconnection)
}