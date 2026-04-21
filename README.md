# Paas Mark

A benchmarking tool for PaaS platforms that measures CPU, database, Redis, and HTTP performance.

## CLI Mode

The CLI mode allows you to run benchmarks from the command line with results output as CSV to stdout (status messages go to stderr).

### Usage

```bash
paasmark [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--benchmark-type` | `cpu` | Benchmark type: `cpu`, `db`, `redis`, or `http` |
| `--endpoint` | | API endpoint URL (required for cpu, db, redis benchmarks) |
| `--dsn` | | Database connection string (required for db benchmarks) |
| `--redis-dsn` | | Redis connection string (required for redis benchmarks) |
| `--http-url` | | HTTP URL to benchmark (required for http benchmarks) |
| `--concurrency` | `10` | Number of concurrent workers |
| `--duration` | `30` | Benchmark duration in seconds |
| `--query-type` | `mixed` | Query type for db benchmarks: `read`, `write`, or `mixed` |
| `--seed-data-mb` | `10` | MB of test data to seed before db benchmark |

### Benchmark Types

#### CPU Benchmark

Runs on a remote server via the API endpoint. Measures CPU hashing performance and reports available memory.

```bash
paasmark --benchmark-type=cpu --endpoint=https://your-paas-app.example.com
```

Output columns: `cpu_hashes_per_sec`, `cpu_total_hashes`, `cpu_num_cores`, `available_memory_mb`

#### Database Benchmark

Benchmarks database performance through a remote server. Supports read, write, or mixed query patterns.

```bash
paasmark --benchmark-type=db \
  --endpoint=https://your-paas-app.example.com \
  --dsn="postgres://user:pass@host:5432/db" \
  --concurrency=20 \
  --duration=60 \
  --query-type=mixed \
  --seed-data-mb=100
```

Output columns include: `seed_time_s`, `queries_per_sec`, `avg_latency_ns`, `p95_latency_ns`, plus separate read/write stats when applicable.

#### Redis Benchmark

Benchmarks Redis performance through a remote server.

```bash
paasmark --benchmark-type=redis \
  --endpoint=https://your-paas-app.example.com \
  --redis-dsn="redis://host:6379" \
  --concurrency=10 \
  --duration=30
```

Output columns: `redis_keys_per_sec`, `redis_total_keys`, `redis_avg_latency_ns`, `redis_p95_latency_ns`, etc.

#### HTTP Benchmark

Runs locally (no remote server needed). Benchmarks HTTP endpoint performance by making requests to the specified URL.

```bash
paasmark --benchmark-type=http \
  --http-url=https://your-paas-app.example.com \
  --concurrency=50 \
  --duration=30
```

Output columns: `requests_per_sec`, `avg_latency_ns`, `min_latency_ns`, `max_latency_ns`, `p95_latency_ns`, `total_requests`, `errors`

### Output Format

Results are output as CSV to stdout with a header row followed by a values row:

```
cpu_hashes_per_sec,cpu_total_hashes,cpu_num_cores,available_memory_mb
1234567.89,37037037,4,8192
```

Status messages and progress updates are written to stderr, allowing you to pipe CSV output cleanly:

```bash
paasmark --benchmark-type=cpu --endpoint=https://example.com > results.csv
```
