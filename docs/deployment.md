# Deployment Guide

Three deployment models are covered here, in increasing complexity. Start with single node to verify your build works, then move to replicated for high availability, then clustered for horizontal scale.

## Single Node

The simplest way to run GoCache. One process, no replication, no cluster config.

### Starting the server

**With flags only:**
```bash
./gocache --port 7000 --metrics-port 9090
```

**With a config file (recommended):**
```bash
./gocache --config config.yaml
```

A minimal `config.yaml`:
```yaml
port: 7000
metrics_port: 9090
max_cache_size: 1000
role: leader
aof_file: "data/cache.aof"
snapshot_file: "data/cache.rdb"
sync_policy: "everysecond"
snapshot_interval_seconds: 300
log_level: "info"
```

**Required flags:** none — all flags have defaults. The server will start on port `7000` with no config file.

**Commonly overridden:**

| Flag | Default | When to change |
|------|---------|----------------|
| `--port` | `7000` | Running multiple instances on one machine |
| `--max-size` | `1000` | Any real workload |
| `--sync-policy` | `everysecond` | `always` for max durability, `no` for max speed |
| `--log-level` | `info` | `debug` when troubleshooting |

### Verifying it's running

```bash
# Basic connectivity
redis-cli -p 7000 PING
# PONG

# Check the metrics endpoint
curl http://localhost:9090/metrics

# Confirm writes and reads work
redis-cli -p 7000 SET hello world
# OK
redis-cli -p 7000 GET hello
# "world"
```

---

## Replicated Setup

One leader and two followers, running in Docker. The leader accepts all writes and replicates them asynchronously to followers. If the leader goes down, the highest-priority follower promotes itself.

### Starting the cluster

```bash
git clone https://github.com/erickim73/gocache.git
cd gocache
docker compose up --build
```

### What each service does

**`leader`**
```yaml
command: ["gocache", "--role", "leader", "--port", "7000"]
ports:
  - "7000:7000"   # RESP — clients connect here
  - "9090:9090"   # Prometheus metrics
```
Starts in leader role on port 7000. Exposes metrics on 9090. The healthcheck polls `GET /health` every 5 seconds — followers will not start until this passes, enforced by `depends_on: condition: service_healthy`.

**`follower1`**
```yaml
command: ["gocache", "--role", "follower", "--port", "7001",
          "--leader-addr", "leader:8000", "--priority", "2",
          "--repl-port", "8001"]
ports:
  - "7001:7001"   # RESP — serves reads
```
Connects to the leader's replication port (`leader:8000`) and streams changes. `--priority 2` makes this the preferred candidate for promotion — higher priority wins elections.

**`follower2`**
```yaml
command: ["gocache", "--role", "follower", "--port", "7002",
          "--leader-addr", "leader:8000", "--priority", "1",
          "--repl-port", "8002", "--peer-repl-addrs", "follower1:8001"]
ports:
  - "7002:7002"   # RESP — serves reads
```
Same as follower1 but lower priority (`--priority 1`), so it only promotes if follower1 is also unreachable. `--peer-repl-addrs follower1:8001` tells it to check whether follower1 has already promoted before self-promoting, preventing split-brain.

### Verifying replication

```bash
# Write to the leader
redis-cli -p 7000 SET replication_test "hello from leader"
# OK

# Read from each follower — both should return the value
redis-cli -p 7001 GET replication_test
# "hello from leader"

redis-cli -p 7002 GET replication_test
# "hello from leader"

# Confirm followers reject writes
redis-cli -p 7001 SET readonly_test "should fail"
# (error) ERR READONLY You can't write against a read only replica
```

### Failover

**Kill the leader:**
```bash
docker compose stop leader
```

**What happens next:**
1. follower1 and follower2 stop receiving heartbeats from the leader
2. After the election timeout, follower1 detects the failure
3. follower2 checks `--peer-repl-addrs` (follower1:8001) before promoting — since follower1 is reachable, follower2 defers
4. follower1 self-promotes to leader (priority 2 wins)

**Confirm follower1 is now the leader:**
```bash
# follower1 should now accept writes
redis-cli -p 7001 SET post_failover "written after failover"
# OK

# follower2 should have the value (now replicating from follower1)
redis-cli -p 7002 GET post_failover
# "written after failover"

# The old leader port is down
redis-cli -p 7000 PING
# Could not connect to Redis at 127.0.0.1:7000: Connection refused
```

**Restart the original leader (it rejoins as a follower):**
```bash
docker compose start leader
```

---

## Clustered Setup

Cluster mode enables horizontal sharding. Data is distributed across shards using consistent hashing — each key is assigned to exactly one shard based on its hash. If a client sends a command to the wrong shard, it receives a `-MOVED` redirect pointing to the correct node.

Each shard is itself a replication group: one leader and one or more followers. The `nodes` and `shards` fields in your config define this topology.

### Config structure

**`NodeInfo`** — defines a single server process:
```yaml
nodes:
  - id: "node1"         # unique identifier for this node
    host: "localhost"   # hostname or IP
    port: 7000          # RESP port (clients connect here)
    repl_port: 8000     # replication port (followers connect here)
    priority: 2         # election priority — higher wins
    shard_id: "shard1"  # which shard this node belongs to
    role: "leader"      # starting role: leader or follower
```

**`ShardInfo`** — defines a replication group:
```yaml
shards:
  - shard_id: "shard1"    # must match shard_id in NodeInfo
    leader_id: "node1"    # node ID of the shard leader
    followers:            # node IDs of all followers in this shard
      - "node2"
```

### Two-shard cluster config

The following config sets up two shards. Each shard has one leader and one follower — four nodes total. Save one copy per node, changing only `node_id`.

**`config-node1.yaml`** (shard1 leader):
```yaml
port: 7000
metrics_port: 9090
max_cache_size: 10000
aof_file: "data/node1.aof"
snapshot_file: "data/node1.rdb"
sync_policy: "everysecond"
snapshot_interval_seconds: 300
log_level: "info"

node_id: "node1"

nodes:
  - id: "node1"
    host: "localhost"
    port: 7000
    repl_port: 8000
    priority: 2
    shard_id: "shard1"
    role: "leader"
  - id: "node2"
    host: "localhost"
    port: 7001
    repl_port: 8001
    priority: 1
    shard_id: "shard1"
    role: "follower"
  - id: "node3"
    host: "localhost"
    port: 7002
    repl_port: 8002
    priority: 2
    shard_id: "shard2"
    role: "leader"
  - id: "node4"
    host: "localhost"
    port: 7003
    repl_port: 8003
    priority: 1
    shard_id: "shard2"
    role: "follower"

shards:
  - shard_id: "shard1"
    leader_id: "node1"
    followers:
      - "node2"
  - shard_id: "shard2"
    leader_id: "node3"
    followers:
      - "node4"
```

The other three nodes use the same `nodes` and `shards` blocks — only `node_id`, `port`, `metrics_port`, `repl_port`, and the data file paths change per node.

### Starting a two-shard cluster

```bash
# Start all four nodes (each with its own config)
./gocache --config config-node1.yaml &   # shard1 leader  — port 7000
./gocache --config config-node2.yaml &   # shard1 follower — port 7001
./gocache --config config-node3.yaml &   # shard2 leader  — port 7002
./gocache --config config-node4.yaml &   # shard2 follower — port 7003
```

### Verifying cluster routing

```bash
# Write a key — it lands on whichever shard owns it
redis-cli -p 7000 SET mykey "hello"
# OK  (if mykey hashes to shard1)
# or
# (error) MOVED node3 localhost:7002  (if mykey hashes to shard2)

# If you get MOVED, retry on the indicated node
redis-cli -p 7002 SET mykey "hello"
# OK

# Check which nodes are in the cluster
redis-cli -p 7000 CLUSTER NODES
# node1 localhost:7000 alive
# node2 localhost:7001 alive
# node3 localhost:7002 alive
# node4 localhost:7003 alive

# Add a node at runtime (triggers automatic key migration)
redis-cli -p 7000 CLUSTER ADDNODE node5 localhost:7004
# OK
```

### Cluster vs. replicated — when to use each

| | Replicated | Clustered |
|---|---|---|
| **Use when** | Data fits on one machine, need HA | Data exceeds one machine, need scale |
| **Writes** | One leader handles all writes | Distributed across shard leaders |
| **Reads** | Any follower | Any follower in the correct shard |
| **Failover** | Automatic, priority-based | Per-shard, same mechanism |
| **Client handling** | Connect to leader, reads to any follower | Must handle `-MOVED` redirects |
| **Config** | No `nodes`/`shards` needed | Requires full topology in config |