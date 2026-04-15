# Benchmark API

## Launch Benchmark

**POST** `/api/benchmark`

Launches a new benchmark run in the background and returns immediately with a unique ID.

### Request Body

```json
{
  "driver": "pgx",
  "dsn": "postgres://user:pass@host:5432/db",
  "concurrency": 10,
  "duration": 30,
  "query_type": "mixed",
  "seed_data_mb": 100
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `driver` | string | Yes | - | Database driver: `"pgx"` (PostgreSQL) or `"mysql"` |
| `dsn` | string | Yes | - | Database connection string |
| `concurrency` | int | No | 10 | Number of concurrent workers |
| `duration` | int | No | 30 | Benchmark duration in seconds |
| `query_type` | string | No | "mixed" | Query type: `"read"`, `"write"`, or `"mixed"` |
| `seed_data_mb` | int | No | 0 | MB of test data to seed before benchmark |

### Response

**202 Accepted**

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "pending"
}
```

**400 Bad Request**

```json
{
  "error": "driver is required"
}
```

---

## Get Benchmark Status/Result

**GET** `/api/benchmark/:id`

Returns the current status and result of a benchmark run.

### Response

**200 OK** (pending)

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "pending",
  "created_at": "2024-01-15T10:30:00Z"
}
```

**200 OK** (running)

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "running",
  "created_at": "2024-01-15T10:30:00Z"
}
```

**200 OK** (complete)

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "complete",
  "created_at": "2024-01-15T10:30:00Z",
  "result": {
    "InitDuration": 5000000000,
    "TotalDuration": 30000000000,
    "QueriesPerSec": 1500.5,
    "AvgLatency": 6500000,
    "MinLatency": 500000,
    "MaxLatency": 50000000,
    "P95Latency": 15000000,
    "Errors": 0,
    "ErrorMessages": [],
    "ReadStats": {
      "Count": 38000,
      "QueriesPerSec": 1266.67,
      "AvgLatency": 6000000,
      "MinLatency": 500000,
      "MaxLatency": 45000000,
      "P95Latency": 14000000
    },
    "WriteStats": {
      "Count": 7000,
      "QueriesPerSec": 233.33,
      "AvgLatency": 9000000,
      "MinLatency": 1000000,
      "MaxLatency": 50000000,
      "P95Latency": 20000000
    }
  }
}
```

**200 OK** (failed)

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "failed",
  "error": "failed to ping database: connection refused",
  "created_at": "2024-01-15T10:30:00Z"
}
```

**404 Not Found**

```json
{
  "error": "benchmark not found"
}
```

### Status Values

| Status | Description |
|--------|-------------|
| `pending` | Benchmark is queued but not yet started |
| `running` | Benchmark is currently executing |
| `complete` | Benchmark finished successfully |
| `failed` | Benchmark failed with an error |

### Result Fields

All duration/latency values are in nanoseconds.

| Field | Description |
|-------|-------------|
| `InitDuration` | Time to set up test table and seed data |
| `TotalDuration` | Configured benchmark duration |
| `QueriesPerSec` | Overall queries per second |
| `AvgLatency` | Average query latency |
| `MinLatency` | Minimum query latency |
| `MaxLatency` | Maximum query latency |
| `P95Latency` | 95th percentile latency |
| `Errors` | Number of query errors |
| `ErrorMessages` | First 10 error messages |
| `ReadStats` | Statistics for read queries only |
| `WriteStats` | Statistics for write queries only |
