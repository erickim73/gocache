package config

import (
	"time"
)

type Config struct {
	// server settings
	port int

	// cache settings
	MaxCacheSize int

	// persistence settings
	AOFFileName string
	SnapshotFileName string
	SyncPolicy string // SyncAlways, SyncEverySecond, SyncNo
	SnapshotInterval time.Duration
	GrowthFactor int64
}