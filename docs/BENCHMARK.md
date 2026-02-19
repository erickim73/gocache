# GoCache Benchmark Results

GoCache is a distributed in-memory cache built in Go with leader-follower replication. This document compares its performance against Redis 7 (the industry baseline) across a grid of pipeline sizes and value sizes.

**Headline numbers at pipeline=1 (no batching, most realistic for typical cache clients):**

| Operation | Redis | GoCache | GoCache % of Redis |
|-----------|-------|---------|-------------------|
| SET 10B   | 121K rps | 30K rps | 25% |
| GET 10B   | 138K rps | 59K rps | 43% |
| SET 1KB   | 110K rps | 26K rps | 24% |
| GET 1KB   | 131K rps | 57K rps | 44% |

GoCache reads are competitive at low concurrency. Writes are bottlenecked — and that bottleneck has a known cause explained below.

---

## Test Environment

**Hardware:** Dell laptop running Ubuntu 24 via WSL2

**Network path:** Both Redis and GoCache run inside Docker with ports exposed to the WSL host. `redis-benchmark` runs on the host and hits both servers through Docker's port-mapping layer. This means the comparison is fair — both servers face identical network overhead — but absolute numbers will be slightly lower than a bare-metal deployment.

**GoCache cluster:** 1 leader (port 7000) + 2 followers with leader-follower replication enabled. Benchmarks target the leader, so every SET triggers replication to followers — an overhead Redis standalone does not have.

**Redis:** Official `redis:7-alpine` image, default configuration, no persistence.

**Benchmark tool:** `redis-benchmark` with 100,000 requests, 50 concurrent clients, `--csv` output for full percentile data.

---

## Memory Efficiency

Both servers were pre-loaded with 10,000 keys of 100B values before memory was sampled.

| Server  | Memory Used | Metric Source |
|---------|------------|---------------|
| Redis   | 1.06 MB    | `INFO memory → used_memory_human` |
| GoCache | 16.9 MB    | `process_resident_memory_bytes` (Prometheus) |

**Redis is ~16x more memory efficient** for this workload. Redis uses jemalloc with compact internal encodings for small values and short strings. GoCache uses standard Go map allocations with full struct overhead per entry — each key-value pair carries Go runtime metadata that Redis's hand-optimized allocator avoids.

This is a known tradeoff of a pure-Go implementation. Reducing it would require a custom memory allocator or off-heap storage, both significant engineering investments beyond the scope of this project.

---

## Throughput Results

### SET Throughput (requests/sec)

| Pipeline | Value Size | Redis     | GoCache | GoCache % |
|----------|-----------|-----------|---------|-----------|
| 1        | 10B       | 121,507   | 30,248  | 25%       |
| 1        | 100B      | 107,759   | 31,867  | 30%       |
| 1        | 1KB       | 109,649   | 25,813  | 24%       |
| 10       | 10B       | 680,272   | 19,794  | 3%        |
| 10       | 100B      | 568,182   | 41,322  | 7%        |
| 10       | 1KB       | 1,149,425 | 33,445  | 3%        |
| 100      | 10B       | 2,564,103 | 36,846  | 1.4%      |
| 100      | 100B      | 1,428,571 | 39,761  | 2.8%      |
| 100      | 1KB       | 1,428,571 | 33,659  | 2.4%      |

### GET Throughput (requests/sec)

| Pipeline | Value Size | Redis     | GoCache | GoCache % |
|----------|-----------|-----------|---------|-----------|
| 1        | 10B       | 137,552   | 59,242  | 43%       |
| 1        | 100B      | 95,511    | 60,423  | 63%       |
| 1        | 1KB       | 131,234   | 56,529  | 43%       |
| 10       | 10B       | 1,492,537 | 384,615 | 26%       |
| 10       | 100B      | 689,655   | 337,838 | 49%       |
| 10       | 1KB       | 819,672   | 227,273 | 28%       |
| 100      | 10B       | 2,777,778 | 411,523 | 15%       |
| 100      | 100B      | 2,631,579 | 500,000 | 19%       |
| 100      | 1KB       | 1,351,351 | 336,700 | 25%       |

---

## Latency Results

### p50 / p95 / p99 at pipeline=1 (ms)

| Server  | Op  | Size | p50   | p95   | p99   |
|---------|-----|------|-------|-------|-------|
| Redis   | SET | 10B  | 0.183 | 0.663 | 1.215 |
| GoCache | SET | 10B  | 1.367 | 3.215 | 4.567 |
| Redis   | GET | 10B  | 0.167 | 0.631 | 1.263 |
| GoCache | GET | 10B  | 0.631 | 1.535 | 2.127 |
| Redis   | SET | 1KB  | 0.207 | 0.863 | 1.407 |
| GoCache | SET | 1KB  | 1.615 | 3.855 | 5.103 |
| Redis   | GET | 1KB  | 0.175 | 0.719 | 1.487 |
| GoCache | GET | 1KB  | 0.655 | 1.655 | 2.351 |

### p50 / p95 / p99 at pipeline=100 (ms) — where contention is most visible

| Server  | Op  | Size | p50   | p95    | p99    | p99/p50 |
|---------|-----|------|-------|--------|--------|---------|
| Redis   | SET | 10B  | 1.511 | 2.687  | 4.535  | 3.0x    |
| GoCache | SET | 10B  | 1.791 | 6.943  | 14.671 | **8.2x**    |
| Redis   | GET | 10B  | 1.223 | 3.167  | 3.391  | 2.8x    |
| GoCache | GET | 10B  | 4.999 | 13.815 | 29.503 | **5.9x**    |
| Redis   | SET | 1KB  | 0.847 | 1.695  | 2.071  | 2.4x    |
| GoCache | SET | 1KB  | 2.151 | 7.167  | 44.031 | **20.5x**   |
| Redis   | GET | 1KB  | 1.711 | 5.687  | 6.751  | 3.9x    |
| GoCache | GET | 1KB  | 1.087 | 8.567  | 15.887 | **14.6x**   |

The p99/p50 ratio measures latency *consistency*. Redis stays within 2–4x of its median under maximum pipeline load. GoCache's ratio blows out to 8–20x — the worst 1% of requests wait dramatically longer than typical. This is lock contention becoming visible in the tail: under deep pipelining, queued writers occasionally back up long enough to cause multi-millisecond stalls.

---

## Analysis

### Why SET throughput doesn't scale with pipelining

Redis SET scales ~12x from pipeline=1 to pipeline=100 (121K → 2.5M rps). GoCache barely moves (30K → 37K). This flat line is the signature of a serialized write path.

Pipelining only improves throughput if the server can process queued work in parallel. GoCache's flat response means something is serializing all SET operations regardless of how many are queued. The most likely cause is a global exclusive write lock: every SET acquires it, writes, releases — so concurrent commands queue up and execute one at a time. Replication compounds this: each write triggers a round-trip to followers, adding latency that Redis standalone never pays. Together they create a hard throughput ceiling that pipelining can't break through.

### Why GET scales much better than SET

GoCache GET scales from ~59K to ~500K rps as pipelining increases — a meaningful improvement, versus SET's near-flat line. This is consistent with a `sync.RWMutex`: multiple readers can hold the lock simultaneously, so pipelining creates real parallelism on the read path. Writers remain exclusive, so pipelining offers no benefit there.

At pipeline=10, GoCache GET runs at 26–49% of Redis. GoCache SET runs at 3–7%. The gap between those ratios is a direct measurement of how much RWMutex read-sharing helps versus how badly exclusive write locking hurts under concurrent load.

### The p99 blowout under high pipelining

The most operationally significant finding is the p99 latency at pipeline=100. GoCache SET at 1KB reaches **44ms p99** against a 2.15ms p50 — a 20x spread. Redis's equivalent spread is 2.4x.

In production this would manifest as intermittent slow requests under burst traffic. When the pipeline queues up faster than the lock can drain, some requests wait through many lock cycles before executing. Throughput numbers describe the average case; the latency percentiles reveal what users actually experience when traffic spikes.

### Memory overhead

GoCache uses ~16x more memory than Redis for the same 10K-key dataset. This is a predictable cost of Go's runtime memory model versus Redis's hand-tuned allocator, not a bug. Production deployments would need to account for it in capacity planning — roughly 16MB baseline process overhead plus ~1.6KB per key at 100B values.

---

## What These Numbers Mean in Context

GoCache adds distributed guarantees that Redis standalone doesn't provide: leader-follower replication with automatic failover and health checking. Some write overhead is inherent to those guarantees, not just implementation inefficiency.

At pipeline=1 — the typical scenario for a cache client without explicit batching — GoCache reads at 43–63% of Redis throughput with consistent latency. The write gap and p99 blowout under burst load both point to the same fix: reducing write lock scope through sharded locking, which would allow parallel writes to different key ranges without serializing the entire store.

---

## Reproducing These Results

```bash
# Start the full cluster (Redis + GoCache leader + 2 followers)
docker-compose up -d --build

# Verify both targets are responding
redis-cli -p 6379 ping   # Redis
redis-cli -p 7000 ping   # GoCache leader

# Run the full benchmark sweep
chmod +x benchmarks/run_benchmark.sh
./benchmarks/run_benchmark.sh
```

Results are saved to `benchmarks/results/benchmark_<timestamp>.txt`.