<div align="center">

# GoCache

A Redis-compatible distributed cache built from scratch in Go — featuring leader-follower replication, horizontal sharding via consistent hashing, pub/sub, and MULTI/EXEC transactions.

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
<!-- [![License](https://img.shields.io/badge/license-MIT-blue?style=flat)](LICENSE) -->
<!-- [![Protocol](https://img.shields.io/badge/protocol-RESP-red?style=flat)](docs/api.md) -->

[Features](#features) • [Architecture](#architecture) • [Quick Start](#quick-start) • [Performance](#performance) • [Documentation](#documentation)

</div>

---

## Features

**Core**
- Redis wire protocol (RESP) — works with `redis-cli` and any standard Redis client out of the box
- `GET`, `SET`, `DEL`, `DBSIZE`, `PING` — fully implemented with correct RESP encoding
- TTL expiration via `SET key value EX seconds`, with auto-eviction at expiry
- LRU eviction — bounded cache with configurable max size, O(1) eviction using a hash map + doubly-linked list

**Persistence**
- AOF (append-only log) — configurable sync policy: `always`, `everysecond`, or `no`
- RDB snapshots — point-in-time binary snapshots at configurable intervals
- Crash recovery via snapshot + AOF replay on startup

**Replication & High Availability**
- Leader-follower replication — async propagation; followers reject writes with `READONLY`
- Automatic failover — priority-based election; highest-priority reachable follower self-promotes
- Health checking via heartbeat; configurable election timeout

**Distribution**
- Consistent hashing across cluster nodes — minimal key movement when scaling
- `-MOVED` redirect responses for cross-shard routing, matching Redis Cluster semantics

**Messaging & Transactions**
- Pub/Sub — `SUBSCRIBE`, `UNSUBSCRIBE`, `PUBLISH`, `PSUBSCRIBE` with full subscriber mode enforcement
- Transactions — `MULTI`/`EXEC`/`DISCARD` with pre-execution validation and `EXECABORT` on queue errors

**Observability**
- Prometheus metrics endpoint — latency histograms, active connections, operation counts
- Structured logging with configurable level (`debug`, `info`, `warn`, `error`)
- YAML-based configuration for full cluster topology, shards, roles, and persistence

---

## Architecture

```
                           ┌──────────────────┐
                           │      Client      │
                           │   (redis-cli)    │
                           └────────┬─────────┘
                                    │  RESP
              ┌─────────────────────┼─────────────────────┐
              │                     │                     │
              ▼                     ▼                     ▼
     ┌──────────────────┐      -MOVED redirect     ┌──────────────────┐
     │  Leader  Shard 1 │ ◀─────────────────────▶ │  Leader  Shard 2 │
     │  :7000   (RESP)  │                          │  :7001   (RESP)  │
     │  :7010   (repl)  │      consistent hash     │  :7011   (repl)  │
     │  :9090 (metrics) │                          │  :9091 (metrics) │
     └────────┬─────────┘                          └──────────────────┘
              │
              │  async replication
              │
     ┌────────▼─────────┐
     │  Follower Node   │   read-only replica; self-promotes to leader
     │  :7002   (RESP)  │   on heartbeat timeout via priority election
     │  :7012   (repl)  │
     │  :9092 (metrics) │
     └──────────────────┘
```

Writes are handled by the shard leader. Cross-shard requests receive a `-MOVED` redirect pointing the client to the correct node. Followers replicate asynchronously and serve reads. On leader failure, the highest-priority reachable follower promotes itself.

---

## Quick Start

```bash
git clone https://github.com/erickim73/gocache.git
cd gocache
docker-compose up --build
```

```bash
# Basic read/write
redis-cli -p 7000 SET hello world EX 60
redis-cli -p 7000 GET hello

# Verify replication reached the follower
redis-cli -p 7002 GET hello

# Pub/Sub — run in two separate terminals
redis-cli -p 7000 SUBSCRIBE news          # terminal 1: enter subscriber mode
redis-cli -p 7000 PUBLISH news "update"  # terminal 2: publish a message
```

---

## Performance

Benchmarked with `redis-benchmark` against a single GoCache leader node.  
**Command:** `redis-benchmark -p 7000 -n 100000 -c 50 -P 16`  
**Hardware:** <!-- e.g. MacBook Pro M2, 16GB RAM, local loopback -->

| Operation | GoCache    | Redis 7.x   | Notes                       |
|-----------|------------|-------------|-----------------------------|
| `GET`     | 54,000 RPS | 110,000 RPS | —                           |
| `SET`     | 49,000 RPS | 105,000 RPS | with AOF `everysecond` sync |
| `PING`    | 61,000 RPS | 130,000 RPS | —                           |

The gap vs. Redis is expected. Redis runs a single-threaded event loop backed by epoll/kqueue with decades of I/O optimization. GoCache uses one goroutine per connection — a simpler model that trades some throughput for readability. At ~50% of Redis throughput for a ground-up Go implementation, the numbers are competitive.

---

## Project Structure

```
gocache/
├── cmd/
│   └── server/          # Entry point: flag parsing, config loading, server startup
├── internal/
│   ├── cache/           # Core LRU cache with TTL, thread-safe Get/Set/Delete
│   ├── persistence/     # AOF append-only log + RDB snapshot writer/reader
│   ├── replication/     # Leader struct: follower tracking, async replication stream
│   ├── pubsub/          # Channel subscriptions, pattern matching, message fanout
│   └── config/          # YAML config loading, flag parsing, cluster topology helpers
├── pkg/
│   └── protocol/        # RESP parser and encoder (arrays, bulk strings, errors, integers)
└── server/
    ├── commands.go      # All command handlers: SET, GET, DEL, MULTI/EXEC, SUBSCRIBE, etc.
    ├── cluster.go       # Cluster command routing, MOVED redirects, consistent hashing
    └── node_state.go    # Runtime node state: role, leader reference, cluster membership
```

---

## Documentation

| | |
|---|---|
| [API Reference](docs/api.md) | All supported commands, syntax, return values, and error codes |
| [Deployment Guide](docs/deployment.md) | Single node, leader-follower, and full cluster setup |
| [Configuration Reference](docs/configuration.md) | Every config file field and CLI flag with defaults |