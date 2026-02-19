# Configuration Reference

GoCache can be configured via a YAML config file, CLI flags, or both. This document covers every available option, the precedence rule when both are used, and a complete annotated example config.

## Precedence

**CLI flags win over config file values.**

The load order is:

1. Defaults are applied (hardcoded in `DefaultConfig`)
2. The config file is loaded and overwrites defaults
3. Any CLI flags that were explicitly passed overwrite what the config file set

This means you can keep a shared `config.yaml` for your base configuration and override individual values at launch without editing the file:

```bash
# Use the config file, but override the log level for this run
./gocache --config config.yaml --log-level debug
```

The `--config` flag itself defaults to `config.yaml` in the working directory. If the file is not found, GoCache logs a warning and continues with defaults — it does not exit.

---

## CLI Flags

All flags are optional. Defaults are shown in the table.

### Server

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--config` | string | `config.yaml` | Path to YAML config file |
| `--port` | int | `7000` | RESP server port — clients connect here |
| `--metrics-port` | int | `9090` | Prometheus metrics port |

### Cache

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--max-size` | int | `1000` | Maximum number of keys before LRU eviction begins |

### Replication

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--role` | string | `leader` | Node role: `leader` or `follower` |
| `--leader-addr` | string | `localhost:7001` | Replication address of the leader — used by followers only |
| `--repl-port` | int | `0` | Port for the replication listener — followers connect here. If `0`, the OS assigns a port |
| `--priority` | int | `0` | Election priority — higher value is preferred during failover |
| `--peer-repl-addrs` | string | `""` | Comma-separated list of peer replication addresses to check before self-promoting, preventing split-brain |

### Persistence

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--aof-file` | string | `cache.aof` | Path to the append-only log file |
| `--snapshot-file` | string | `cache.rdb` | Path to the snapshot file |
| `--sync-policy` | string | `everysecond` | AOF sync frequency: `always`, `everysecond`, or `no` |
| `--growth-factor` | int64 | `2` | AOF rewrite trigger — rewrites the log when it grows by this multiple |
| `--snapshot-interval` | int (seconds) | `300` | How often to write a full snapshot to disk |

### Cluster

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--node-id` | string | `""` | This node's ID — must match an entry in the `nodes` list in the config file. Required for cluster mode |

---

## YAML Config Fields

All fields are optional. Unset fields fall back to the defaults listed above.

### Server

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | int | `7000` | RESP server port |
| `metrics_port` | int | `9090` | Prometheus metrics port |

### Cache

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_cache_size` | int | `1000` | Maximum number of keys |

### Replication

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `role` | string | `leader` | `leader` or `follower` |
| `leader_addr` | string | `localhost:7001` | Leader's replication address — followers only |
| `repl_port` | int | `0` | Replication listener port — followers connect here. If `0`, the OS assigns a port |

### Persistence

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `aof_file` | string | `cache.aof` | AOF file path |
| `snapshot_file` | string | `cache.rdb` | Snapshot file path |
| `sync_policy` | string | `everysecond` | `always`, `everysecond`, or `no` |
| `snapshot_interval_seconds` | int | `300` | Snapshot interval in seconds |
| `growth_factor` | int64 | `2` | AOF rewrite growth factor |

### Cluster

| Field | Type | Description |
|-------|------|-------------|
| `node_id` | string | This node's ID — must match an entry in `nodes` |
| `nodes` | list of `NodeInfo` | Full list of every node in the cluster |
| `shards` | list of `ShardInfo` | Replication group definitions |
| `log_level` | string | `debug`, `info`, `warn`, or `error` |

### NodeInfo fields

Each entry in the `nodes` list describes one server process.

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier for this node |
| `host` | string | Hostname or IP address |
| `port` | int | RESP port |
| `repl_port` | int | Replication listener port |
| `priority` | int | Election priority — higher wins |
| `shard_id` | string | Which shard this node belongs to |
| `role` | string | Starting role: `leader` or `follower` |

### ShardInfo fields

Each entry in the `shards` list defines a replication group.

| Field | Type | Description |
|-------|------|-------------|
| `shard_id` | string | Shard identifier — must match `shard_id` in the relevant `NodeInfo` entries |
| `leader_id` | string | Node ID of the shard leader |
| `followers` | list of string | Node IDs of all followers in this shard |

### Sync policy values

| Value | Behavior | Durability | Performance |
|-------|----------|------------|-------------|
| `always` | fsync after every write | Strongest — zero data loss | Slowest |
| `everysecond` | fsync once per second | At most 1 second of writes lost on crash | Balanced (recommended) |
| `no` | OS decides when to flush | Weakest | Fastest |

---

## Complete Annotated Example

Copy this file and adjust values for your environment. Every field is shown.

```yaml
# ── Server ────────────────────────────────────────────────────────────────────

# RESP port — clients connect here (redis-cli, any Redis client library)
port: 7000

# Prometheus metrics endpoint: GET http://localhost:9090/metrics
metrics_port: 9090


# ── Cache ─────────────────────────────────────────────────────────────────────

# Maximum number of keys. When this limit is reached, least-recently-used
# keys are evicted to make room for new ones.
max_cache_size: 10000


# ── Replication ───────────────────────────────────────────────────────────────

# Role of this node. Set to "follower" on replica nodes.
role: leader

# Address of the leader's replication port. Only used when role is "follower".
# Format: host:repl_port  (not the RESP port)
leader_addr: "localhost:8000"

# Port that followers connect to for replication streaming.
# Must match the port used in leader_addr on follower nodes.
# If 0, the OS assigns an available port (not recommended in production).
repl_port: 8000


# ── Persistence ───────────────────────────────────────────────────────────────

# Path to the append-only log file.
# Every write command is appended here for crash recovery.
aof_file: "data/cache.aof"

# Path to the binary snapshot file.
# Written periodically as a full point-in-time copy of the cache.
snapshot_file: "data/cache.rdb"

# How often to fsync the AOF to disk.
# "always"      — safest, slowest (fsync on every write)
# "everysecond" — recommended (at most 1 second of data loss)
# "no"          — fastest, least safe (OS decides when to flush)
sync_policy: "everysecond"

# How often (in seconds) to write a full snapshot.
snapshot_interval_seconds: 300

# AOF rewrite growth factor.
# When the AOF file is this many times larger than the last rewrite baseline,
# it is compacted. A value of 2 means rewrite when the file doubles in size.
growth_factor: 2


# ── Logging ───────────────────────────────────────────────────────────────────

# Log verbosity. Options: debug, info, warn, error
# Use "debug" when troubleshooting replication or cluster routing issues.
log_level: "info"


# ── Cluster (omit this entire section for single-node or replicated setup) ────

# The ID of this specific node. Must match one entry in the nodes list below.
# This is how GoCache knows which port to bind and which shard it belongs to.
node_id: "node1"

# Full topology of the cluster. Every node in the cluster must be listed here,
# on every node's config file. All nodes must have identical nodes/shards blocks.
nodes:
  - id: "node1"
    host: "localhost"
    port: 7000          # RESP port for this node
    repl_port: 8000     # Replication port — followers connect here
    priority: 2         # Higher = preferred leader during election
    shard_id: "shard1"  # Which shard this node belongs to
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

# Replication groups. Each shard has exactly one leader and one or more followers.
# shard_id must match the shard_id values used in the nodes list above.
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

---

## Common Configurations

**Minimal single node (testing):**
```yaml
port: 7000
max_cache_size: 1000
sync_policy: "no"
log_level: "debug"
```

**Production single node:**
```yaml
port: 7000
metrics_port: 9090
max_cache_size: 100000
repl_port: 8000
aof_file: "/var/data/gocache/cache.aof"
snapshot_file: "/var/data/gocache/cache.rdb"
sync_policy: "everysecond"
snapshot_interval_seconds: 60
log_level: "warn"
```

**Follower node (replicated setup):**
```yaml
port: 7001
metrics_port: 9091
max_cache_size: 100000
role: follower
leader_addr: "leader-host:8000"
repl_port: 8001
aof_file: "/var/data/gocache/cache.aof"
snapshot_file: "/var/data/gocache/cache.rdb"
sync_policy: "everysecond"
snapshot_interval_seconds: 60
log_level: "info"
```