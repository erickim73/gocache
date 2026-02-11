package main

import (
	"fmt"
	
	"github.com/erickim73/gocache/internal/config"
)

func main() {
	// load defaults
	cfg := config.DefaultConfig()

	// parse flags
	configFile := config.ParseFlags(cfg)

	// load config file
	fileCfg, err := config.LoadFromFile(configFile)
	if err != nil {
		fmt.Printf("Could not load config file '%s', using defaults: %v\n", configFile, err)
	} else {
		cfg = fileCfg
	}

	// apply flags after loading file
	config.ApplyFlags(cfg)

	// start metrics server 
	go StartMetricsServer(cfg.MetricsPort)

	// determine which mode we're in 
	if cfg.IsClusterMode() {
		fmt.Println("Starting in CLUSTER mode")
		startClusterMode(cfg)
	} else {
		fmt.Println("Starting in SIMPLE mode")
		startSimpleMode(cfg)
	}
}