package config

import (
	"time"

	// "gopkg.in/yaml.v3"
)

type yamlConfig struct {
	Port int `yaml:"port"`
	MaxCacheSize int `yaml:"max_cache_size"`
	AOFFIleName string `yaml:"aof_file"`
	SnapshotFileName string `yaml:"snapshot_file"`
	SyncPolicy string `yaml:"sync_policy"`
	SnapshotIntervalSeconds int `yaml:"snapshot_interval_seconds"`
	GrowthFactor int64 `yaml:"growth_factor"`
}

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