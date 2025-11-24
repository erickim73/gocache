# GoCache Benchmark Results

## Test Environment
- CPU: Intel Core Ultra 7 165U
- OS: Linux (WSL)
- Go version: 1.25.3
- Date: 11/23/2025

## Results

| Mode | ns/op | ops/sec | Slowdown vs No Persistence |
|------|-------|---------|---------------------------|
| No Persistence | 414 | 2,415,459 | 1x (baseline) |
| SyncNo | 539,112 | 1,855 | ~1,300x |
| SyncEverySecond | 558,523 | 1,790 | ~1,350x |
| SyncAlways | 2,279,402 | 439 | ~5,500x |

## Analysis

### WSL Impact
These benchmarks were run on WSL, which has slower file I/O than native Linux. 
On native systems, expect:
- SyncNo to be significantly faster than SyncEverySecond
- Overall persistence overhead to be lower

### Recommendations

- **Development/Testing**: Use `SyncNo` or no persistence
- **Production (most cases)**: Use `SyncEverySecond` - good durability/performance balance
- **Critical data**: Use `SyncAlways` - accepts ~5x slower writes for guaranteed durability

### Durability vs Performance Tradeoff

The core tradeoff: **faster writes = more data at risk during crashes**

- `SyncAlways`: Zero data loss, but ~5,500x slower
- `SyncEverySecond`: May lose 1 second of writes, ~1,350x slower
- `SyncNo`: May lose more data, ~1,300x slower
- No persistence: Lose everything, but fastest