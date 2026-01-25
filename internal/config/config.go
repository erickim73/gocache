package config

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/erickim73/gocache/internal/persistence"
	"gopkg.in/yaml.v3"
)

type yamlConfig struct {
	Port                    int    `yaml:"port"`
	MaxCacheSize            int    `yaml:"max_cache_size"`
	Role 					string `yaml:"role"`
	LeaderAddr 				string `yaml:"leader_addr"`
	AOFFIleName             string `yaml:"aof_file"`
	SnapshotFileName        string `yaml:"snapshot_file"`
	SyncPolicy              string `yaml:"sync_policy"`
	SnapshotIntervalSeconds int    `yaml:"snapshot_interval_seconds"`
	GrowthFactor            int64  `yaml:"growth_factor"`
}

type Config struct {
	// server settings
	Port int

	// cache settings
	MaxCacheSize int

	// replication settings
	Role string // leader or follower
	LeaderAddr string

	// persistence settings
	AOFFileName      string
	SnapshotFileName string
	SyncPolicy       string // always, everysecond, no
	SnapshotInterval time.Duration
	GrowthFactor     int64
}

func DefaultConfig() *Config {
	return &Config{
		Port:             7000,
		MaxCacheSize:     1000,
		Role: 			  "leader",
		LeaderAddr: 	  "localhost:7000",
		AOFFileName:      "cache.aof",
		SnapshotFileName: "cache.rdb",
		SyncPolicy:       "everysecond",
		SnapshotInterval: 5 * time.Minute,
		GrowthFactor:     2,
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
		Port:             yc.Port,
		MaxCacheSize:     yc.MaxCacheSize,
		Role: 			  yc.Role,
		LeaderAddr: 	  yc.LeaderAddr,
		AOFFileName:      yc.AOFFIleName,
		SnapshotFileName: yc.SnapshotFileName,
		SyncPolicy:       yc.SyncPolicy,
		SnapshotInterval: time.Duration(yc.SnapshotIntervalSeconds) * time.Second,
		GrowthFactor:     yc.GrowthFactor,
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

func ParseFlags(cfg *Config) string {
	// config file path flag
	configFile := flag.String("config", "config.yaml", "Path to config file")

	// server flags
	flag.IntVar(&cfg.Port, "port", cfg.Port, "Server port")

	// cache flags
	flag.IntVar(&cfg.MaxCacheSize, "max-size", cfg.MaxCacheSize, "Maximum cache size")

	// replication flags
	flag.StringVar(&cfg.Role, "role", cfg.Role, "Role: leader or follower")
	flag.StringVar(&cfg.LeaderAddr, "leader-addr", cfg.LeaderAddr, "Leader address (host: port)")
	

	// persistence flags
	flag.StringVar(&cfg.AOFFileName, "aof-file", cfg.AOFFileName, "AOF File path")
	flag.StringVar(&cfg.SnapshotFileName, "snapshot-file", cfg.SnapshotFileName, "Snapshot file path")
	flag.StringVar(&cfg.SyncPolicy, "sync-policy", cfg.SyncPolicy, "Sync policy: always, everysecond, no")
	flag.Int64Var(&cfg.GrowthFactor, "growth-factor", cfg.GrowthFactor, "AOF rewrite growth factor")

	// snapshot interval
	snapshotSeconds := flag.Int("snapshot-interval", int(cfg.SnapshotInterval.Seconds()), "Snapshot interval in seconds")

	flag.Parse()

	// convert snapshot seconds to duration
	cfg.SnapshotInterval = time.Duration(*snapshotSeconds) * time.Second

	return *configFile
}

func ApplyFlags(cfg *Config) {
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			fmt.Sscanf(f.Value.String(), "%d", &cfg.Port)
		case "max-size":
			fmt.Sscanf(f.Value.String(), "%d", &cfg.MaxCacheSize)
		case "role":
			cfg.Role = f.Value.String()
		case "leader-addr":
			cfg.LeaderAddr = f.Value.String()
		case "aof-file":
			cfg.AOFFileName = f.Value.String()
		case "snapshot-file":
			cfg.SnapshotFileName = f.Value.String()
		case "sync-policy":
			cfg.SyncPolicy = f.Value.String()
		case "growth-factor":
			fmt.Sscanf(f.Value.String(), "%d", &cfg.GrowthFactor)
		case "snapshot-interval":
			var seconds int
			fmt.Sscanf(f.Value.String(), "%d", &seconds)
			cfg.SnapshotInterval = time.Duration(seconds) * time.Second
		}
	})
}
