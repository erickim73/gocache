package main

import (
	"os"
	"testing"

	"github.com/erickim73/gocache/internal/config"
)

func TestClusterConfig(t *testing.T) {
	// CHANGED: write a cluster-mode config to a temp file instead of relying
	// on a relative path to config.yaml, which breaks in CI where the working
	// directory is the repo root, not cmd/server/
	content := `
port: 7000
metrics_port: 9090
max_cache_size: 1000
log_level: "info"
nodes:
  - id: "node1"
    host: "localhost"
    port: 7001
    repl_port: 8001
    priority: 1
  - id: "node2"
    host: "localhost"
    port: 7002
    repl_port: 8002
    priority: 2
`
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp config: %v", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := config.LoadFromFile(f.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	cfg.NodeID = "node1"

	if !cfg.IsClusterMode() {
		t.Fatalf("expected cluster mode")
	}

	myNode, err := cfg.GetMyNode()
	if err != nil {
		t.Fatalf("failed to get my node: %v", err)
	}

	if myNode.ID != "node1" {
		t.Fatalf("expected node1, got %s", myNode.ID)
	}
}