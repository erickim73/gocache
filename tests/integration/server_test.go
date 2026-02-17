package integration_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/erickim73/gocache/tests/harness"
)

// confirms the full wiring from harness -> server.New() -> real TCP connection is working
func TestSmokePing(t *testing.T) {
	h := harness.New(t)

	leader := h.StartLeader()

	client := h.Client(leader)

	got := client.Ping()

	if got != "PONG" {
		t.Errorf("expected PONG, got %q", got)
	}
}				
