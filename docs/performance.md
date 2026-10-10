# Performance

Benchmarks of Djinn's store, transport, and wish updates under realistic workloads, using the
reproducible wish generator (`internal/testx/bigwish`).

## Workloads

The benchmarks evaluate two reproducible workloads:
- **`real`**: ~70 tasks, questions, decisions, and tilasms matching a developer's real-world wish (~82 KiB serialized Protobuf).
- **`x10`**: Ten times larger (~700 tasks, questions, blocks, and tilasms, ~737 KiB serialized Protobuf).

The benchmarks live in `internal/server/wish_bench_test.go` under the `headless` build tag:
1. **Store Read (`BenchmarkWishStoreRead`)**: Direct read of a whole wish from the store (`store.Get`, tasks, questions, blocks, tilasms, and azima fills).
2. **Server Send (`BenchmarkWishServerSend`)**:
   - `proto`: Pure binary Protobuf serialization of the wish's task list.
   - `unary`: Full `TaskService.List` round trip over the Unix socket.
   - `stream`: Delivery of a watch update over a Connect server stream.
3. **Change vs. Incremental (`BenchmarkWishChange`)**: Evaluates the cost of a state transition (`progress` or `done`), comparing:
   - `reread`: Re-reading the entire wish and broadcasting the full list to clients.
   - `incremental`: Emitting only the delta (the modified task or question).

## Running the benchmarks

Run through Task, passing `go test` flags after `--`:

```bash
# Run all wish benchmarks
go tool task bench -- -bench Wish

# Quick smoke run (1 iteration per benchmark)
go tool task bench -- -bench Wish -benchtime 1x

# Run a specific benchmark with memory statistics
go tool task bench -- -bench BenchmarkWishStoreRead -benchmem

# Run multiple passes for statistical comparison
go tool task bench -- -bench Wish -count 5
```

> [!NOTE]
> Like standard Go benchmarks, these benchmarks block nothing and enforce no hard latency thresholds (budgets belong in end-to-end specs).

## Profiling

To capture a CPU profile for any benchmark:

```bash
go tool task bench -- -bench BenchmarkWishStoreRead/real -benchtime 5s -cpuprofile cpu.prof
```

Inspect the profile interactively or as a summary:

```bash
# Top functions by CPU time
go tool pprof -top cpu.prof

# Interactive web interface with flamegraphs and call graphs
go tool pprof -http=:8080 cpu.prof
```

To capture memory allocation profiles, replace `-cpuprofile cpu.prof` with `-memprofile mem.prof` and inspect with `go tool pprof -alloc_space mem.prof`.

## Comparing runs with benchstat

To measure optimizations and detect regressions without noise, use [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat) (`BSD-3-Clause` license).

1. Record baseline before changes:
   ```bash
   go tool task bench -- -bench Wish -count 10 > old.txt
   ```
2. Record measurements after changes:
   ```bash
   go tool task bench -- -bench Wish -count 10 > new.txt
   ```
3. Compare the two runs:
   ```bash
   # Run via go run (or install via go install golang.org/x/perf/cmd/benchstat@latest)
   go run golang.org/x/perf/cmd/benchstat@latest old.txt new.txt
   ```

`benchstat` calculates delta percentages and p-values to verify statistical significance.

## Baseline measurements

**Environment:** Intel Core i7-1360P (16 threads), Ubuntu 22.04, Linux amd64, Go 1.26.7. Median of `-count 3` with `-benchmem`.

### Whole wish store read

Direct store retrieval of wish metadata, task graph, questions, decision blocks, and tilasms with azima fills.

| Size | Time per read | Throughput | Memory / op | Allocs / op |
| :--- | :--- | :--- | :--- | :--- |
| **`real`** | 3.61 ms | 79.4 MB/s | 1.27 MB | 8,459 |
| **`x10`** | 58.4 ms | 48.9 MB/s | 12.76 MB | 82,441 |

### Server serialization and delivery

What the server sends to the window when reading or updating the task list.

| Size | Mechanism | Time / op | Wire bytes | Memory / op | Allocs / op |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`real`** | `proto` (marshal) | 0.69 ms | 81.9 KB | 81.9 KB | 1 |
| | `unary` (RPC call) | 6.31 ms | 81.9 KB | 1.40 MB | 11,131 |
| | `stream` (watch push) | 0.71 ms | 433 B | 14.0 KB | 220 |
| **`x10`** | `proto` (marshal) | 4.64 ms | 737 KB | 737 KB | 1 |
| | `unary` (RPC call) | 47.7 ms | 737 KB | 14.0 MB | 108,285 |
| | `stream` (watch push) | 0.98 ms | 480 B | 45.4 KB | 270 |

### Wish updates: re-read vs. incremental

Comparison of re-reading and broadcasting the whole wish on each change versus emitting an incremental delta.

| Workload | Strategy | Time / op | Wire bytes / op | Memory / op | Allocs / op |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`real` / progress** | `reread` | 10.17 ms | 78,097 B | 2.22 MB | 17,156 |
| | **`incremental`** | **0.73 ms** | **435 B** | **14.5 KB** | **224** |
| **`real` / done** | `reread` | 10.32 ms | 78,532 B | 2.28 MB | 17,395 |
| | **`incremental`** | **4.23 ms** | **1,166 B** | **711 KB** | **5,680** |
| **`x10` / progress** | `reread` | 96.90 ms | 763,814 B | 23.1 MB | 165,782 |
| | **`incremental`** | **1.58 ms** | **524 B** | **58.3 KB** | **366** |
| **`x10` / done** | `reread` | 69.40 ms | 764,427 B | 23.1 MB | 166,212 |
| | **`incremental`** | **22.20 ms** | **2,900 B** | **6.93 MB** | **51,974** |

### Key takeaways (Q63 hypothesis)

- **Re-reading on each change scales linearly with total wish size**: On a `x10` wish, re-reading on every small task progress change takes ~97 ms, generates 764 KiB of wire traffic, and allocates 23 MB across 165,000 objects.
- **Incremental dispatch scales with the change**: Transmitting incremental updates takes ~1.58 ms, only 524 wire bytes, and allocates 58 KB (a **60x speedup** and **1,450x reduction in wire traffic**).
