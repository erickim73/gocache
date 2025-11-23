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
	Port int

	// cache settings
	MaxCacheSize int

	// persistence settings
	AOFFileName string
	SnapshotFileName string
	SyncPolicy string // SyncAlways, SyncEverySecond, SyncNo
	SnapshotInterval time.Duration
	GrowthFactor int64
}

func DefaultConfig() *Config {
	return &Config {
		Port: 6379,
		MaxCacheSize: 1000,
		AOFFileName: "cache.aof",
		SnapshotFileName: "cache.rdb",
		SyncPolicy: "SyncEverySecond",
		SnapshotInterval: 5 * time.Minute,
		GrowthFactor: 2,
	}
}