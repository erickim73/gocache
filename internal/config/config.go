package config

import (
	"time"
	"os"

	"gopkg.in/yaml.v3"
	"github.com/erickim73/gocache/internal/persistence"
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
	SyncPolicy string // always, everysecond, no
	SnapshotInterval time.Duration
	GrowthFactor int64
}

func DefaultConfig() *Config {
	return &Config {
		Port: 6379,
		MaxCacheSize: 1000,
		AOFFileName: "cache.aof",
		SnapshotFileName: "cache.rdb",
		SyncPolicy: "everysecond",
		SnapshotInterval: 5 * time.Minute,
		GrowthFactor: 2,
	}
}

func LoadFromFile(fileName string) (*Config, error) {
	data, err := os.ReadFile(fileName)
	if err != nil {
		return nil, err
	}

	var yc yamlConfig
	err = yaml.Unmarshal(data, &yc)
	if err != nil {
		return nil, err
	}

	// convert to config struct
	return &Config{
		Port: yc.Port,
		MaxCacheSize: yc.MaxCacheSize,
		AOFFileName: yc.AOFFIleName,
		SnapshotFileName: yc.SnapshotFileName,
		SyncPolicy: yc.SyncPolicy,
		SnapshotInterval: time.Duration(yc.SnapshotIntervalSeconds) * time.Second,
		GrowthFactor: yc.GrowthFactor,
	}, nil
}

func (c *Config) GetSyncPolicy() persistence.SyncPolicy {
	switch c.SyncPolicy {
	case "always":
		return persistence.SyncAlways
	case "everysecond":
		return persistence.SyncEverySecond
	case "no":
		return persistence.SyncNo
	default:
		return persistence.SyncEverySecond
	}
}