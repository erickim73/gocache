package integration_test

import (
	"testing"
	"time"

	"github.com/erickim73/gocache/tests/harness"
)

// verifies that when a leader dies, a follower detects the failure and promotes itself to accept writes
func TestLeaderFailoverBasic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping failover test in short mode (takes ~20 seconds)")
	}

	h := harness.New(t)

	// start leader + 1 follower
	nodes := h.StartCluster(1)
	leader := nodes[0]
	follower := nodes[1]

	leaderClient := h.Client(leader)
	followerClient := h.Client(follower)
	
	// write a key to the leader and verify it replicates
	if got := leaderClient.Set("pre-failover-key", "pre-failover-value"); got != "OK" {
		t.Fatalf("leader SET failed: got %q", got)
	}

	harness.EventuallyEqual(t, 5 * time.Second, "pre-failover-value", func() string {
		return followerClient.Get("pre-failover-key")
	})

	// kill leader ungracefully
	t.Logf("Killing leader at %s", time.Now().Format("15:05:05"))
	leader.Kill()

	// wait for follower to detect leader death and promote itself
	promoted := false
	harness.EventuallyTrue(t, 30*time.Second, "follower promotes to leader", func() bool {
		// try to write to follower. if it's now the leader, set will succeed
		got := followerClient.Set("post-failover-key", "post-failover-value")

		// a successful write means the follower has promoted
		if got == "OK" {
			if !promoted {
				t.Logf("Follower promoted to leader at %s", time.Now().Format("15:04:05"))
				promoted = true
			}
			return true
		}

		// still waiting for promotion
		return false
	})

	// verify new leader can serve reads too
	if got := followerClient.Get("post-failover-key"); got != "post-failover-value" {
		t.Errorf("new leader GET: expected post-failover-value, got %q", got)
	}

	// verify pre-failover data is still there
	if got := followerClient.Get("pre-failover-key"); got != "pre-failover-value" {
		t.Errorf("new leader GET old key: expected pre-failover-value, got %q", got)
	}
}

// verifies that when multiple followers exist, only one promotes itself to leader
func TestMultipleFollowersElection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-follower failover test in short mode (takes ~25 seconds)")
	}

	h := harness.New(t)

	// start leader + 2 followers.
	nodes := h.StartCluster(2)
	leader := nodes[0]
	follower1 := nodes[1]
	follower2 := nodes[2]

	leaderClient := h.Client(leader)
	follower1Client := h.Client(follower1)
	follower2Client := h.Client(follower2)

	// write and replicate a key before failover
	leaderClient.Set("multi-follower-key", "multi-follower-value")
	harness.EventuallyEqual(t, 5*time.Second, "multi-follower-value", func() string {
		return follower1Client.Get("multi-follower-key")
	})

	// kill the leader.
	t.Logf("Killing leader at %s", time.Now().Format("15:04:05"))
	leader.Kill()

	// wait for one follower to promote. We don't know which one will win so we poll both until one accepts writes
	var newLeaderClient *harness.Client
	harness.EventuallyTrue(t, 35*time.Second, "one follower becomes leader", func() bool {
		// try writing to follower1
		if follower1Client.Set("election-test-key", "election-test-value") == "OK" {
			if newLeaderClient == nil {
				t.Logf("Follower1 became leader at %s", time.Now().Format("15:04:05"))
				newLeaderClient = follower1Client
			}
			return true
		}

		// try writing to follower2
		if follower2Client.Set("election-test-key", "election-test-value") == "OK" {
			if newLeaderClient == nil {
				t.Logf("Follower2 became leader at %s", time.Now().Format("15:04:05"))
				newLeaderClient = follower2Client
			}
			return true
		}

		return false
	})

	// verify the new leader has the data.
	if newLeaderClient == nil {
		t.Fatal("no follower promoted to leader")
	}

	if got := newLeaderClient.Get("election-test-key"); got != "election-test-value" {
		t.Errorf("new leader GET: expected election-test-value, got %q", got)
	}

	// verify pre-failover data survived.
	if got := newLeaderClient.Get("multi-follower-key"); got != "multi-follower-value" {
		t.Errorf("new leader GET old key: expected multi-follower-value, got %q", got)
	}
}

// verifies that after a follower promotes to leader, new writes are accepted and the system continues operating normally
func TestReplicationAfterFailover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping post-failover replication test in short mode (takes ~20 seconds)")
	}

	h := harness.New(t)

	nodes := h.StartCluster(1)
	leader := nodes[0]
	follower := nodes[1]

	leaderClient := h.Client(leader)
	followerClient := h.Client(follower)

	// write before failover
	leaderClient.Set("before-failover", "value1")
	harness.EventuallyEqual(t, 5*time.Second, "value1", func() string {
		return followerClient.Get("before-failover")
	})

	// kill the leader
	leader.Kill()

	// wait for follower to promote
	harness.EventuallyTrue(t, 30*time.Second, "follower accepts writes", func() bool {
		return followerClient.Set("after-failover", "value2") == "OK"
	})

	// verify both keys exist on the new leader
	if got := followerClient.Get("before-failover"); got != "value1" {
		t.Errorf("expected value1, got %q", got)
	}
	if got := followerClient.Get("after-failover"); got != "value2" {
		t.Errorf("expected value2, got %q", got)
	}

	// write another key to verify ongoing functionality
	if got := followerClient.Set("after-promotion", "value3"); got != "OK" {
		t.Fatalf("new leader SET failed: got %q", got)
	}

	if got := followerClient.Get("after-promotion"); got != "value3" {
		t.Errorf("expected value3, got %q", got)
	}

	// test DEL to ensure all operations work
	if got := followerClient.Del("after-failover"); got != "1" {
		t.Fatalf("new leader DEL failed: got %q", got)
	}

	if got := followerClient.Get("after-failover"); got != "" {
		t.Errorf("expected empty string after DEL, got %q", got)
	}
}

// verifies that the follower correctly detects when the leader is no longer sending heartbeats
func TestLeaderDeathDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heartbeat timeout test in short mode (takes ~20 seconds)")
	}

	h := harness.New(t)

	nodes := h.StartCluster(1)
	leader := nodes[0]
	follower := nodes[1]

	followerClient := h.Client(follower)

	// record the time when we kill the leader
	killTime := time.Now()
	t.Logf("Killing leader at %s", killTime.Format("15:04:05"))
	leader.Kill()

	// wait for the follower to promote. measure how long it takes
	var promotionTime time.Time
	harness.EventuallyTrue(t, 30*time.Second, "follower promotes", func() bool {
		if followerClient.Set("detection-test", "value") == "OK" {
			if promotionTime.IsZero() {
				promotionTime = time.Now()
				elapsed := promotionTime.Sub(killTime)
				t.Logf("Follower promoted after %v (kill time: %s, promotion time: %s)",
					elapsed, killTime.Format("15:04:05"), promotionTime.Format("15:04:05"))
			}
			return true
		}
		return false
	})

	// verify the promotion took at least 15 seconds (the heartbeat timeout)
	elapsed := promotionTime.Sub(killTime)
	if elapsed < 14*time.Second {
		t.Errorf("promotion happened too quickly: %v (expected >= 14s due to 15s heartbeat timeout)", elapsed)
	}

	// also verify it didn't take absurdly long 
	if elapsed > 25*time.Second {
		t.Errorf("promotion took too long: %v (expected ~15-20s)", elapsed)
	}
}