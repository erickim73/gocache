package config

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/erickim73/gocache/internal/persistence"
	"gopkg.in/yaml.v3"
)
type NodeInfo struct {
	ID       string `yaml:"id"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	ReplPort int    `yaml:"repl_port"`
	Priority int    `yaml:"priority"`
	ShardID  string `yaml:"shard_id"`
	Role     string `yaml:"role"`
}

type yamlConfig struct {
	Port                    int         `yaml:"port"`
	MetricsPort 			int 	    `yaml:"metrics_port"`
	MaxCacheSize            int         `yaml:"max_cache_size"`
	Role 					string      `yaml:"role"`
	LeaderAddr 				string      `yaml:"leader_addr"`
	ReplPort 				int 		`yaml:"repl_port"`
	AOFFileName             string      `yaml:"aof_file"`
	SnapshotFileName        string      `yaml:"snapshot_file"`
	SyncPolicy              string      `yaml:"sync_policy"`
	SnapshotIntervalSeconds int         `yaml:"snapshot_interval_seconds"`
	GrowthFactor            int64       `yaml:"growth_factor"`
    
	// cluster fields to YAML parsing
	NodeID                  string      `yaml:"node_id"`
	Nodes                   []NodeInfo  `yaml:"nodes"`
	Shards 					[]ShardInfo `yaml:"shards"`
	LogLevel 				string 		`yaml:"log_level"` // debug, info, warn, error
}

type Config struct {
	// server settings
	Port int
	
	// metrics settings
	MetricsPort int

	// cache settings
	MaxCacheSize int

	// replication settings
	Role string // leader or follower
	LeaderAddr string
	Priority int
	ReplPort int
	PeerReplAddrs string 

	// persistence settings
	AOFFileName      string
	SnapshotFileName string
	SyncPolicy       string // always, everysecond, no
	SnapshotInterval time.Duration
	GrowthFactor     int64

	// cluster settings
	NodeID           string
	Nodes            []NodeInfo

	// shard settings
	Shards []ShardInfo 

	// logging statistics
	LogLevel string
}

// defines a replication group
type ShardInfo struct {
	ShardID string `yaml:"shard_id"` // e.g., "shard1"
	LeaderID string `yaml:"leader_id"` // nodeID of leader
	Followers []string `yaml:"followers"` // node IDs of followers
}

func DefaultConfig() *Config {
	return &Config{
		Port:             7000,
		MetricsPort: 	  9090,
		MaxCacheSize:     1000,
		Role: 			  "leader",
		LeaderAddr: 	  "localhost:7001",
		AOFFileName:      "cache.aof",
		SnapshotFileName: "cache.rdb",
		SyncPolicy:       "everysecond",
		SnapshotInterval: 5 * time.Minute,
		GrowthFactor:     2,
		NodeID:           "",
		Nodes:            []NodeInfo{},
		LogLevel: 		  "info",
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
		MetricsPort: 	  yc.MetricsPort,
		MaxCacheSize:     yc.MaxCacheSize,
		Role: 			  yc.Role,
		LeaderAddr: 	  yc.LeaderAddr,
		ReplPort: 	      yc.ReplPort,
		AOFFileName:      yc.AOFFileName,
		SnapshotFileName: yc.SnapshotFileName,
		SyncPolicy:       yc.SyncPolicy,
		SnapshotInterval: time.Duration(yc.SnapshotIntervalSeconds) * time.Second,
		GrowthFactor:     yc.GrowthFactor,
		NodeID:           yc.NodeID,
		Nodes:            yc.Nodes,
		Shards: 		  yc.Shards,
		LogLevel:         yc.LogLevel,
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
	flag.IntVar(&cfg.MetricsPort, "metrics_port", cfg.MetricsPort, "Metrics port")

	// cache flags
	flag.IntVar(&cfg.MaxCacheSize, "max-size", cfg.MaxCacheSize, "Maximum cache size")

	// replication flags
	flag.StringVar(&cfg.Role, "role", cfg.Role, "Role: leader or follower")
	flag.StringVar(&cfg.LeaderAddr, "leader-addr", cfg.LeaderAddr, "Leader address (host: port)")
	flag.IntVar(&cfg.Priority, "priority", cfg.Priority, "Election priority (higher = preferred leader)")
	flag.IntVar(&cfg.ReplPort, "repl-port", cfg.ReplPort, "Replication port override")
	flag.StringVar(&cfg.PeerReplAddrs, "peer-repl-addrs", cfg.PeerReplAddrs, "Comma-separated peer replication addresses to check before self-promoting")

	flag.StringVar(&cfg.NodeID, "node-id", cfg.NodeID, "Node ID for cluster mode")

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
		case "metrics-port":
			fmt.Sscanf(f.Value.String(), "%d", &cfg.MetricsPort)
		case "max-size":
			fmt.Sscanf(f.Value.String(), "%d", &cfg.MaxCacheSize)
		case "role":
			cfg.Role = f.Value.String()
		case "leader-addr":
			cfg.LeaderAddr = f.Value.String()
		case "priority":
			fmt.Sscanf(f.Value.String(), "%d", &cfg.Priority)
		case "repl-port":
			fmt.Sscanf(f.Value.String(), "%d", &cfg.ReplPort)
		case "peer-repl-addrs":
			cfg.PeerReplAddrs = f.Value.String()
		case "node-id":
			cfg.NodeID = f.Value.String()
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

// helper function for seeing if cluster mode is enabled
func (c *Config) IsClusterMode() bool {
	return len(c.Nodes) > 0 && c.NodeID != ""
}

// helper function for getting node info from cluster config
func (c *Config) GetMyNode() (*NodeInfo, error) {
	for i := range c.Nodes {
		if c.Nodes[i].ID == c.NodeID {
			return &c.Nodes[i], nil
		}
	}

	return nil, fmt.Errorf("node %s not found in cluster config", c.NodeID)
}

// helper function for getting other nodes
func (c *Config) GetOtherNodes() []NodeInfo {
	others := []NodeInfo{}
	for _, node := range c.Nodes {
		if node.ID != c.NodeID {
			others = append(others, node)
		}
	}
	return others
}

// helper function for finding highest priority node
func (c *Config) GetHighestPriorityNode() *NodeInfo {
	if len(c.Nodes) == 0 {
		return nil
	}

	highest := &c.Nodes[0]
	for i := range c.Nodes {
		if c.Nodes[i].Priority > highest.Priority {
			highest = &c.Nodes[i]
		}
	}

	return highest
}	

// helper function for seeing if node is highest priority
func (c *Config) AmIHighestPriority() bool {
	myNode, err := c.GetMyNode()
	if err != nil {
		return false
	}

	highest := c.GetHighestPriorityNode()
	return highest != nil && highest.ID == myNode.ID
}

// returns which shard a node belongs to
func (c *Config) GetShardForNode(nodeID string) (*ShardInfo, error) {
	for i := range c.Shards {
		shard := &c.Shards[i]
		if shard.LeaderID == nodeID {
			return shard, nil
		}

		for _, follower := range shard.Followers {
			if follower == nodeID {
				return shard, nil
			}
		}
	}
	return nil, fmt.Errorf("node %s not found in any shard", nodeID)
}

// returns a shard by its id
func (c *Config) GetShardByID(shardID string) (*ShardInfo, error) {
	for i := range c.Shards {
		if c.Shards[i].ShardID == shardID {
			return &c.Shards[i], nil
		}
	}
	return nil, fmt.Errorf("shard %s not found", shardID)
}

// checks if this node is a shard leader
func (c *Config) IsShardLeader() bool {
	for _, shard := range c.Shards {
		if shard.LeaderID == c.NodeID {
			return true
		}
	}
	return false
}

// returns "leader" or "follower"
func (c *Config) GetMyShardRole() string {
	shard, err := c.GetShardForNode(c.NodeID)
	if err != nil {
		return ""
	}

	if shard.LeaderID == c.NodeID {
		return "leader"
	}
	return "follower"
}