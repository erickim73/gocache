package integration_test

import (
	"testing"
	"time"

	"github.com/erickim73/gocache/tests/harness"
)

// verifies that data written to leader is asynchronously replicated to all followers
func TestLeaderToFollowerReplication(t *testing.T) {
	h := harness.New(t)

	// StartCluster(1) returns [leader, follower1]
	nodes := h.StartCluster(1)
	leader := nodes[0]
	follower := nodes[1]

	leaderClient := h.Client(leader)
	followerClient := h.Client(follower)

	// write to leader
	if got := leaderClient.Set("replicated-key", "replicated-value"); got != "OK" {
		t.Fatalf("leader SET failed: got %q", got)
	}

	// poll the follower until the value appears
	harness.EventuallyEqual(t, 3*time.Second, "replicated-value", func() string {
		return followerClient.Get("replicated-key")
	})

	// verify leader still has the value too
	if got := leaderClient.Get("replicated-key"); got != "replicated-value" {
		t.Errorf("leader: expected replicated-value after replication, got %q", got)
	}
}

// verifies that DEL commands are replicated from leader to followers
func TestDeleteReplication(t *testing.T) {
	h := harness.New(t)

	nodes := h.StartCluster(1)
	leader := nodes[0]
	follower := nodes[1]

	leaderClient := h.Client(leader)
	followerClient := h.Client(follower)

	// write to leader.
	if got := leaderClient.Set("delete-test-key", "delete-test-value"); got != "OK" {
		t.Fatalf("leader SET failed: got %q", got)
	}

	// wait for the SET to replicate to the follower.
	harness.EventuallyEqual(t, 3*time.Second, "delete-test-value", func() string {
		return followerClient.Get("delete-test-key")
	})

	// delete on leader.
	if got := leaderClient.Del("delete-test-key"); got != "OK" {
		t.Fatalf("leader DEL failed: got %q", got)
	}

	// verify deletion replicated to follower. The key should become empty string
	harness.EventuallyEqual(t, 3*time.Second, "", func() string {
		return followerClient.Get("delete-test-key")
	})

	// leader should also have the key deleted.
	if got := leaderClient.Get("delete-test-key"); got != "" {
		t.Errorf("leader: expected empty string after DEL, got %q", got)
	}
}

// verifies that data replicates to all followers simultaneously
func TestMultipleFollowers(t *testing.T) {
	h := harness.New(t)

	// Start leader + 2 followers.
	nodes := h.StartCluster(2)
	leader := nodes[0]
	follower1 := nodes[1]
	follower2 := nodes[2]

	leaderClient := h.Client(leader)
	follower1Client := h.Client(follower1)
	follower2Client := h.Client(follower2)

	// Write to leader.
	if got := leaderClient.Set("multi-key", "multi-value"); got != "OK" {
		t.Fatalf("leader SET failed: got %q", got)
	}

	// Both followers should receive the replication.
	harness.EventuallyEqual(t, 3*time.Second, "multi-value", func() string {
		return follower1Client.Get("multi-key")
	})

	harness.EventuallyEqual(t, 3*time.Second, "multi-value", func() string {
		return follower2Client.Get("multi-key")
	})
}

// verifies that keys with TTL are replicated with their expiration times preserved
func TestTTLReplication(t *testing.T) {
	h := harness.New(t)

	nodes := h.StartCluster(1)
	leader := nodes[0]
	follower := nodes[1]

	leaderClient := h.Client(leader)
	followerClient := h.Client(follower)

	// set with 2 second TTL on leader.
	if got := leaderClient.SetEx("ttl-replicate", "ttl-value", 2); got != "OK" {
		t.Fatalf("leader SET with TTL failed: got %q", got)
	}

	// wait for replication to complete.
	harness.EventuallyEqual(t, 3*time.Second, "ttl-value", func() string {
		return followerClient.Get("ttl-replicate")
	})

	// wait for TTL to expire. Use 2.5s to give the cache time to notice expiration
	time.Sleep(2500 * time.Millisecond)

	// verify key is gone from both leader and follower.
	if got := leaderClient.Get("ttl-replicate"); got != "" {
		t.Errorf("leader: expected key to expire, still got %q", got)
	}

	if got := followerClient.Get("ttl-replicate"); got != "" {
		t.Errorf("follower: expected key to expire, still got %q", got)
	}
}

// verifies that when a follower connects, it receives a snapshot of all existing keys on leader
func TestInitialSyncSnapshot(t *testing.T) {
	h := harness.New(t)

	// start just the leader first.
	leader := h.StartLeader()
	leaderClient := h.Client(leader)

	// write some keys to the leader before any followers exist.
	leaderClient.Set("pre-existing-1", "value1")
	leaderClient.Set("pre-existing-2", "value2")
	leaderClient.Set("pre-existing-3", "value3")

	// now start a follower. It should receive a snapshot of all 3 keys during SYNC.
	follower := h.StartFollower(leader)
	followerClient := h.Client(follower)

	// poll for each key to verify the snapshot was received.
	harness.EventuallyEqual(t, 3*time.Second, "value1", func() string {
		return followerClient.Get("pre-existing-1")
	})

	harness.EventuallyEqual(t, 3*time.Second, "value2", func() string {
		return followerClient.Get("pre-existing-2")
	})

	harness.EventuallyEqual(t, 3*time.Second, "value3", func() string {
		return followerClient.Get("pre-existing-3")
	})
}

// verifies that writes to the leader continue to replicate correctly after the initial snapshot has been sent
func TestReplicationAfterSnapshot(t *testing.T) {
	h := harness.New(t)

	nodes := h.StartCluster(1)
	leader := nodes[0]
	follower := nodes[1]

	leaderClient := h.Client(leader)
	followerClient := h.Client(follower)

	// write a key and wait for initial sync.
	leaderClient.Set("initial-key", "initial-value")
	harness.EventuallyEqual(t, 3*time.Second, "initial-value", func() string {
		return followerClient.Get("initial-key")
	})

	// write another key after sync is complete. This tests ongoing replication.
	leaderClient.Set("after-sync-key", "after-sync-value")
	harness.EventuallyEqual(t, 3*time.Second, "after-sync-value", func() string {
		return followerClient.Get("after-sync-key")
	})

	// update the first key to test that overwrites replicate.
	leaderClient.Set("initial-key", "updated-value")
	harness.EventuallyEqual(t, 3*time.Second, "updated-value", func() string {
		return followerClient.Get("initial-key")
	})
}