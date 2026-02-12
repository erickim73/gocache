package main

import (
	"log/slog"
	
	"github.com/erickim73/gocache/internal/config"
	"github.com/erickim73/gocache/internal/logger"
)

func main() {
	// initialize logger
	logger.InitLogger(logger.LevelInfo)	

	// load defaults
	cfg := config.DefaultConfig()

	// parse flags
	configFile := config.ParseFlags(cfg)

	// load config file
	fileCfg, err := config.LoadFromFile(configFile)
	if err != nil {
		slog.Warn("Could not load config file, using defaults",
			"file", configFile,
			"error", err,
		)
	} else {
		cfg = fileCfg
	}

	// apply flags after loading file
	config.ApplyFlags(cfg)

	// re-initialize logger only if log level changed
	if cfg.LogLevel != "info" {
		logger.InitLogger(logger.LogLevel(cfg.LogLevel))
	}

	slog.Info("Configuration loaded",
		"mode", func() string {
			if cfg.IsClusterMode() {
				return "cluster"
			}
			return "simple"
		}(), 
		"port", cfg.Port,
		"metrics_port", cfg.MetricsPort,
	)

	// start metrics server 
	go StartMetricsServer(cfg.MetricsPort)

	// determine which mode we're in 
	if cfg.IsClusterMode() {
		slog.Info("Starting in CLUSTER mode", "node_id", cfg.NodeID)
		startClusterMode(cfg)
	} else {
		slog.Info("Starting in SIMPLE mode")
		startSimpleMode(cfg)
	}
}