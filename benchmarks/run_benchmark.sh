# Runs redis-benchmark against both Redis and GoCache across a grid of pipeline sizes and data sizes, then prints a side-by-side summary.

# ── Config ────────────────────────────────────────────────────────────────────
REDIS_PORT=6379
GOCACHE_PORT=7000
METRICS_PORT=9090          # GoCache Prometheus metrics port
REQUESTS=100000
CONCURRENCY=50
PIPELINES=(1 10 100)       # sweep pipeline sizes
SIZES=(10 100 1024)        # sweep data sizes (bytes)
COMMANDS="get,set"
MEMORY_PRELOAD_KEYS=10000  # number of keys to load before memory snapshot

# Output file — timestamped so reruns don't overwrite previous results
RESULTS_DIR="$(dirname "$0")/results"
mkdir -p "$RESULTS_DIR"
OUTPUT="$RESULTS_DIR/benchmark_$(date +%Y%m%d_%H%M%S).txt"

# ── Health checks before benchmarking ─────────────────────────────────────────
echo "Verifying servers are up..."

# Check Redis
if ! redis-cli -p "$REDIS_PORT" ping > /dev/null 2>&1; then
  echo "ERROR: Redis not responding on port $REDIS_PORT. Aborting."
  exit 1
fi

# Check GoCache
if ! redis-cli -p "$GOCACHE_PORT" ping > /dev/null 2>&1; then
  echo "ERROR: GoCache not responding on port $GOCACHE_PORT. Aborting."
  exit 1
fi

echo "Both servers healthy. Starting benchmark..."
echo "Results will be saved to: $OUTPUT"
echo ""

# ── Memory snapshot ────────────────────────────────────────────────────────────
# Capture memory usage after pre-loading data so we measure memory per
# stored key, not just empty-process baseline. Redis uses INFO memory; GoCache
# doesn't implement that command so we pull process_resident_memory_bytes from
# its Prometheus /metrics endpoint instead.

capture_memory() {
  # Pre-load both servers with the same number of fixed-size keys
  echo "Pre-loading $MEMORY_PRELOAD_KEYS keys for memory measurement..." | tee -a "$OUTPUT"
  redis-benchmark -p "$REDIS_PORT"   -n "$MEMORY_PRELOAD_KEYS" -c 10 -t set -d 100 -q > /dev/null 2>&1
  redis-benchmark -p "$GOCACHE_PORT" -n "$MEMORY_PRELOAD_KEYS" -c 10 -t set -d 100 -q > /dev/null 2>&1

  # Redis: used_memory_human is the amount Redis itself tracks as in-use
  local redis_mem
  redis_mem=$(redis-cli -p "$REDIS_PORT" INFO memory 2>/dev/null | grep "^used_memory_human" | cut -d: -f2 | tr -d '[:space:]')

  # GoCache: process_resident_memory_bytes from Prometheus is the OS-level
  # RSS — the fairest equivalent to Redis's used_memory. gocache_memory_bytes only
  # tracks stored data and reads near-zero on a fresh start, so we avoid it here.
  local gocache_mem_bytes
  gocache_mem_bytes=$(curl -s "http://localhost:$METRICS_PORT/metrics" \
    | grep "^process_resident_memory_bytes" \
    | awk '{print $2}')

  # Convert bytes to MB for readability (printf handles scientific notation)
  local gocache_mem_mb
  gocache_mem_mb=$(echo "$gocache_mem_bytes" | awk '{printf "%.1fM", $1/1048576}')

  echo "=== Memory Usage (after $MEMORY_PRELOAD_KEYS x 100B keys) ===" | tee -a "$OUTPUT"
  echo "  [redis   ] used_memory: ${redis_mem:-unavailable}"           | tee -a "$OUTPUT"
  echo "  [gocache ] resident:    ${gocache_mem_mb:-unavailable}"      | tee -a "$OUTPUT"
  echo ""                                                               | tee -a "$OUTPUT"
}

capture_memory

# ── Benchmark sweep ────────────────────────────────────────────────────────────
run_bench() {
  local port=$1
  local pipeline=$2
  local size=$3

  # [CHANGED] Replaced -q with --csv so redis-benchmark outputs a header row
  # plus one CSV line per command containing rps and all latency percentiles:
  # "test","rps","avg_ms","min_ms","p50_ms","p95_ms","p99_ms","max_ms"
  # We skip the header row (grep -v "^\"test\""), then extract columns
  # 1,2,5,6,7 (test, rps, p50, p95, p99) with cut, and strip surrounding
  # quotes with tr so the output is clean for display.
  redis-benchmark \
    -p "$port" \
    -n "$REQUESTS" \
    -c "$CONCURRENCY" \
    -P "$pipeline" \
    -d "$size" \
    -t "$COMMANDS" \
    --csv 2>/dev/null \
    | grep -v "^\"test\"" \
    | cut -d',' -f1,2,5,6,7 \
    | tr -d '"'
}

for pipeline in "${PIPELINES[@]}"; do
  for size in "${SIZES[@]}"; do
    
    # Format size label for readability
    if [ "$size" -ge 1024 ]; then
      size_label="$((size / 1024))KB"
    else
      size_label="${size}B"
    fi

    header="=== pipeline=${pipeline} | size=${size_label} ==="
    echo "$header" | tee -a "$OUTPUT"

    # Print column headers so the output is self-documenting
    printf "  %-10s %-6s %-12s %-10s %-10s %-10s\n" \
      "server" "cmd" "rps" "p50_ms" "p95_ms" "p99_ms" | tee -a "$OUTPUT"

    # Run both servers for this combination
    for server in redis gocache; do
      port=$([ "$server" == "redis" ] && echo "$REDIS_PORT" || echo "$GOCACHE_PORT")

      # [CHANGED] awk now parses the cleaned CSV fields (cmd, rps, p50, p95, p99)
      # and formats them into aligned columns alongside the server label.
      # Previously awk just prepended a label to the raw -q line.
      run_bench "$port" "$pipeline" "$size" \
        | awk -v srv="$server" -F',' '
          {
            printf "  %-10s %-6s %-12s %-10s %-10s %-10s\n",
              srv, $1, $2, $3, $4, $5
          }' \
        | tee -a "$OUTPUT"
    done

    echo "" | tee -a "$OUTPUT"
  done
done

echo "Done. Full results saved to $OUTPUT"