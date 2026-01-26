package main

import (
	"testing"

	"github.com/erickim73/gocache/internal/config"
)


func TestClusterConfig(t *testing.T) {
	cfg, err := config.LoadFromFile("config.yaml")
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