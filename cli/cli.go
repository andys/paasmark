package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/andys/paasmark/benchmark"
)

// BenchmarkRequest matches the API request structure
type BenchmarkRequest struct {
	Driver        string `json:"driver"`
	DSN           string `json:"dsn"`
	RedisDSN      string `json:"redis_dsn"`
	Concurrency   int    `json:"concurrency"`
	Duration      int    `json:"duration"`
	QueryType     string `json:"query_type"`
	SeedDataMB    int    `json:"seed_data_mb"`
	BenchmarkType string `json:"benchmark_type"`
}

// BenchmarkStatus represents the current state of a benchmark run
type BenchmarkStatus string

const (
	StatusPending  BenchmarkStatus = "pending"
	StatusRunning  BenchmarkStatus = "running"
	StatusComplete BenchmarkStatus = "complete"
	StatusFailed   BenchmarkStatus = "failed"
)

// QueryStats holds statistics for a category of queries
type QueryStats struct {
	Count         int64   `json:"Count"`
	QueriesPerSec float64 `json:"QueriesPerSec"`
	AvgLatency    int64   `json:"AvgLatency"`
	MinLatency    int64   `json:"MinLatency"`
	MaxLatency    int64   `json:"MaxLatency"`
	P95Latency    int64   `json:"P95Latency"`
}

// Result matches the benchmark.Result structure
type Result struct {
	InitDuration  int64       `json:"InitDuration"`
	TotalDuration int64       `json:"TotalDuration"`
	QueriesPerSec float64     `json:"QueriesPerSec"`
	AvgLatency    int64       `json:"AvgLatency"`
	MinLatency    int64       `json:"MinLatency"`
	MaxLatency    int64       `json:"MaxLatency"`
	P95Latency    int64       `json:"P95Latency"`
	Errors        int64       `json:"Errors"`
	ErrorMessages []string    `json:"ErrorMessages"`
	ReadStats     *QueryStats `json:"ReadStats"`
	WriteStats    *QueryStats `json:"WriteStats"`
}

// CPUResult holds CPU benchmark results
type CPUResult struct {
	Duration          int64   `json:"Duration"`
	TotalHashes       int64   `json:"TotalHashes"`
	HashesPerSec      float64 `json:"HashesPerSec"`
	ThreadCount       int     `json:"ThreadCount"`
	NumCPU            int     `json:"NumCPU"`
	AvailableMemoryMB int64   `json:"AvailableMemoryMB"`
}

// RedisResult holds Redis benchmark results
type RedisResult struct {
	InitDuration  int64    `json:"InitDuration"`
	TotalDuration int64    `json:"TotalDuration"`
	KeysPerSec    float64  `json:"KeysPerSec"`
	AvgLatency    int64    `json:"AvgLatency"`
	MinLatency    int64    `json:"MinLatency"`
	MaxLatency    int64    `json:"MaxLatency"`
	P95Latency    int64    `json:"P95Latency"`
	TotalKeys     int64    `json:"TotalKeys"`
	Errors        int64    `json:"Errors"`
	ErrorMessages []string `json:"ErrorMessages"`
}

// HTTPResult holds HTTP benchmark results
type HTTPResult struct {
	TotalDuration  int64    `json:"TotalDuration"`
	TotalRequests  int64    `json:"TotalRequests"`
	RequestsPerSec float64  `json:"RequestsPerSec"`
	AvgLatency     int64    `json:"AvgLatency"`
	MinLatency     int64    `json:"MinLatency"`
	MaxLatency     int64    `json:"MaxLatency"`
	P95Latency     int64    `json:"P95Latency"`
	Errors         int64    `json:"Errors"`
	ErrorMessages  []string `json:"ErrorMessages"`
	SuccessfulReqs int64    `json:"SuccessfulReqs"`
	BytesReceived  int64    `json:"BytesReceived"`
}

// ResultSet holds the benchmark result along with metadata
type ResultSet struct {
	ID          string          `json:"id"`
	Status      BenchmarkStatus `json:"status"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	Result      *Result         `json:"result,omitempty"`
	CPUResult   *CPUResult      `json:"cpu_result,omitempty"`
	RedisResult *RedisResult    `json:"redis_result,omitempty"`
	HTTPResult  *HTTPResult     `json:"http_result,omitempty"`
}

// Config holds CLI configuration
type Config struct {
	Endpoint      string
	DSN           string
	RedisDSN      string
	HTTPURL       string
	Concurrency   int
	Duration      int
	QueryType     string
	SeedDataMB    int
	BenchmarkType string
}

// Run executes the CLI mode
func Run() error {
	cfg := Config{}

	// Create a custom flag set for CLI mode
	fs := flag.NewFlagSet("paasmark remote", flag.ExitOnError)

	fs.StringVar(&cfg.BenchmarkType, "benchmark-type", "cpu", "Benchmark type: cpu, db, redis, or http")
	fs.StringVar(&cfg.Endpoint, "endpoint", "", "API endpoint URL (required for cpu, db, redis)")
	fs.StringVar(&cfg.DSN, "dsn", "", "Database DSN (required for db)")
	fs.StringVar(&cfg.RedisDSN, "redis-dsn", "", "Redis DSN (required for redis)")
	fs.StringVar(&cfg.HTTPURL, "http-url", "", "Target URL (required for http)")
	fs.IntVar(&cfg.Concurrency, "concurrency", 10, "Number of concurrent workers")
	fs.IntVar(&cfg.Duration, "duration", 30, "Benchmark duration in seconds")
	fs.StringVar(&cfg.QueryType, "query-type", "mixed", "DB query type: read, write, or mixed")
	fs.IntVar(&cfg.SeedDataMB, "seed-data-mb", 10, "MB of test data to seed (db only)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Paasmark - PaaS Benchmarking Tool

Usage:
  paasmark remote [flags]

Benchmark Types:
  cpu    CPU and memory benchmark (runs on remote server)
  db     Database benchmark with configurable read/write patterns
  redis  Redis key/value operations benchmark
  http   HTTP endpoint benchmark (runs locally)

Examples:
  # CPU benchmark
  paasmark remote --benchmark-type=cpu --endpoint=https://app.example.com

  # Database benchmark with PostgreSQL
  paasmark remote --benchmark-type=db --endpoint=https://app.example.com \
    --dsn="postgres://user:pass@host:5432/db" --duration=60

  # Redis benchmark
  paasmark remote --benchmark-type=redis --endpoint=https://app.example.com \
    --redis-dsn="redis://host:6379"

  # HTTP benchmark (local, no remote server needed)
  paasmark remote --benchmark-type=http --http-url=https://app.example.com \
    --concurrency=50 --duration=30

Flags:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Output:
  Results are written to stdout as CSV (header row + values row).
  Status messages are written to stderr.

  Example: paasmark remote --benchmark-type=cpu --endpoint=https://app.example.com > results.csv
`)
	}

	fs.Parse(os.Args[1:])

	// Validate benchmark type
	if cfg.BenchmarkType != "cpu" && cfg.BenchmarkType != "db" && cfg.BenchmarkType != "redis" && cfg.BenchmarkType != "http" {
		return fmt.Errorf("--benchmark-type must be 'cpu', 'db', 'redis', or 'http'")
	}

	// HTTP benchmark runs locally without a remote server
	if cfg.BenchmarkType == "http" {
		if cfg.HTTPURL == "" {
			return fmt.Errorf("--http-url is required for http benchmarks")
		}
		return runHTTPBenchmark(cfg)
	}

	// Validate required fields for remote benchmarks
	if cfg.Endpoint == "" {
		return fmt.Errorf("--endpoint is required for cpu, db, and redis benchmarks")
	}
	// DSN is required for db benchmark type
	if cfg.BenchmarkType == "db" && cfg.DSN == "" {
		return fmt.Errorf("--dsn is required for database benchmarks")
	}
	// Redis DSN is required for redis benchmark type
	if cfg.BenchmarkType == "redis" && cfg.RedisDSN == "" {
		return fmt.Errorf("--redis-dsn is required for redis benchmarks")
	}

	// Normalize endpoint URL
	cfg.Endpoint = strings.TrimSuffix(cfg.Endpoint, "/")

	// Start benchmark
	id, err := startBenchmark(cfg)
	if err != nil {
		return fmt.Errorf("failed to start benchmark: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Benchmark started with ID: %s\n", id)

	// Poll for result
	result, err := pollForResult(cfg, id)
	if err != nil {
		return fmt.Errorf("failed to get result: %w", err)
	}

	// Output CSV
	outputCSV(result)

	return nil
}

func startBenchmark(cfg Config) (string, error) {
	req := BenchmarkRequest{
		DSN:           cfg.DSN,
		RedisDSN:      cfg.RedisDSN,
		Concurrency:   cfg.Concurrency,
		Duration:      cfg.Duration,
		QueryType:     cfg.QueryType,
		SeedDataMB:    cfg.SeedDataMB,
		BenchmarkType: cfg.BenchmarkType,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := http.Post(cfg.Endpoint+"/api/benchmark", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusAccepted {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return "", fmt.Errorf("API error: %s", errResp.Error)
		}
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		ID     string          `json:"id"`
		Status BenchmarkStatus `json:"status"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ID, nil
}

func pollForResult(cfg Config, id string) (*ResultSet, error) {
	url := fmt.Sprintf("%s/api/benchmark/%s", cfg.Endpoint, id)

	for {
		resp, err := http.Get(url)
		if err != nil {
			return nil, fmt.Errorf("failed to poll: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("benchmark not found")
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}

		var rs ResultSet
		if err := json.Unmarshal(body, &rs); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		switch rs.Status {
		case StatusComplete:
			return &rs, nil
		case StatusFailed:
			return nil, fmt.Errorf("benchmark failed: %s", rs.Error)
		case StatusPending, StatusRunning:
			fmt.Fprintf(os.Stderr, "Status: %s, waiting...\n", rs.Status)
			time.Sleep(2 * time.Second)
		default:
			return nil, fmt.Errorf("unknown status: %s", rs.Status)
		}
	}
}

func outputCSV(rs *ResultSet) {
	r := rs.Result
	cpu := rs.CPUResult
	redis := rs.RedisResult

	// Check if we have any results at all
	if r == nil && cpu == nil && redis == nil {
		fmt.Fprintln(os.Stderr, "No result data available")
		return
	}

	// Build header and values as single row
	var headers []string
	var values []string

	// CPU/RAM stats (include memory metric with CPU)
	if cpu != nil {
		headers = append(headers,
			"cpu_hashes_per_sec",
			"cpu_total_hashes",
			"cpu_num_cores",
			"available_memory_mb",
		)
		values = append(values,
			fmt.Sprintf("%.2f", cpu.HashesPerSec),
			fmt.Sprintf("%d", cpu.TotalHashes),
			fmt.Sprintf("%d", cpu.NumCPU),
			fmt.Sprintf("%d", cpu.AvailableMemoryMB),
		)
	}

	// DB stats
	if r != nil {
		headers = append(headers,
			"seed_time_s",
			"total_duration_ns",
			"queries_per_sec",
			"avg_latency_ns",
			"min_latency_ns",
			"max_latency_ns",
			"p95_latency_ns",
			"errors",
		)
		values = append(values,
			fmt.Sprintf("%.2f", float64(r.InitDuration)/1e9),
			fmt.Sprintf("%d", r.TotalDuration),
			fmt.Sprintf("%.2f", r.QueriesPerSec),
			fmt.Sprintf("%d", r.AvgLatency),
			fmt.Sprintf("%d", r.MinLatency),
			fmt.Sprintf("%d", r.MaxLatency),
			fmt.Sprintf("%d", r.P95Latency),
			fmt.Sprintf("%d", r.Errors),
		)

		// Read stats
		if r.ReadStats != nil {
			headers = append(headers,
				"read_count",
				"read_queries_per_sec",
				"read_avg_latency_ns",
				"read_min_latency_ns",
				"read_max_latency_ns",
				"read_p95_latency_ns",
			)
			values = append(values,
				fmt.Sprintf("%d", r.ReadStats.Count),
				fmt.Sprintf("%.2f", r.ReadStats.QueriesPerSec),
				fmt.Sprintf("%d", r.ReadStats.AvgLatency),
				fmt.Sprintf("%d", r.ReadStats.MinLatency),
				fmt.Sprintf("%d", r.ReadStats.MaxLatency),
				fmt.Sprintf("%d", r.ReadStats.P95Latency),
			)
		}

		// Write stats
		if r.WriteStats != nil {
			headers = append(headers,
				"write_count",
				"write_queries_per_sec",
				"write_avg_latency_ns",
				"write_min_latency_ns",
				"write_max_latency_ns",
				"write_p95_latency_ns",
			)
			values = append(values,
				fmt.Sprintf("%d", r.WriteStats.Count),
				fmt.Sprintf("%.2f", r.WriteStats.QueriesPerSec),
				fmt.Sprintf("%d", r.WriteStats.AvgLatency),
				fmt.Sprintf("%d", r.WriteStats.MinLatency),
				fmt.Sprintf("%d", r.WriteStats.MaxLatency),
				fmt.Sprintf("%d", r.WriteStats.P95Latency),
			)
		}
	}

	// Redis stats
	if redis != nil {
		headers = append(headers,
			"redis_init_duration_ns",
			"redis_total_duration_ns",
			"redis_keys_per_sec",
			"redis_total_keys",
			"redis_avg_latency_ns",
			"redis_min_latency_ns",
			"redis_max_latency_ns",
			"redis_p95_latency_ns",
			"redis_errors",
		)
		values = append(values,
			fmt.Sprintf("%d", redis.InitDuration),
			fmt.Sprintf("%d", redis.TotalDuration),
			fmt.Sprintf("%.2f", redis.KeysPerSec),
			fmt.Sprintf("%d", redis.TotalKeys),
			fmt.Sprintf("%d", redis.AvgLatency),
			fmt.Sprintf("%d", redis.MinLatency),
			fmt.Sprintf("%d", redis.MaxLatency),
			fmt.Sprintf("%d", redis.P95Latency),
			fmt.Sprintf("%d", redis.Errors),
		)
	}

	// HTTP stats
	if httpRes := rs.HTTPResult; httpRes != nil {
		headers = append(headers,
			"http_requests_per_sec",
			"http_total_requests",
			"http_avg_latency_ns",
			"http_min_latency_ns",
			"http_max_latency_ns",
			"http_p95_latency_ns",
			"http_errors",
		)
		values = append(values,
			fmt.Sprintf("%.2f", httpRes.RequestsPerSec),
			fmt.Sprintf("%d", httpRes.TotalRequests),
			fmt.Sprintf("%d", httpRes.AvgLatency),
			fmt.Sprintf("%d", httpRes.MinLatency),
			fmt.Sprintf("%d", httpRes.MaxLatency),
			fmt.Sprintf("%d", httpRes.P95Latency),
			fmt.Sprintf("%d", httpRes.Errors),
		)
	}

	// Output header row
	fmt.Println(strings.Join(headers, ","))
	// Output values row
	fmt.Println(strings.Join(values, ","))
}

// runHTTPBenchmark runs the HTTP benchmark locally (not via remote API)
func runHTTPBenchmark(cfg Config) error {
	fmt.Fprintf(os.Stderr, "Running HTTP benchmark against: %s\n", cfg.HTTPURL)
	fmt.Fprintf(os.Stderr, "Concurrency: %d, Duration: %ds\n", cfg.Concurrency, cfg.Duration)

	httpCfg := benchmark.HTTPConfig{
		URL:         cfg.HTTPURL,
		Concurrency: cfg.Concurrency,
		Duration:    time.Duration(cfg.Duration) * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Duration)*time.Second+10*time.Second)
	defer cancel()

	result, err := benchmark.RunHTTP(ctx, httpCfg)
	if err != nil {
		return fmt.Errorf("HTTP benchmark failed: %w", err)
	}

	// Output CSV in the same format as other benchmarks
	outputHTTPCSV(result)

	return nil
}

// outputHTTPCSV outputs HTTP benchmark results as CSV
func outputHTTPCSV(result *benchmark.HTTPResult) {
	headers := []string{
		"requests_per_sec",
		"avg_latency_ns",
		"min_latency_ns",
		"max_latency_ns",
		"p95_latency_ns",
		"total_requests",
		"errors",
	}

	values := []string{
		fmt.Sprintf("%.2f", result.RequestsPerSec),
		fmt.Sprintf("%d", result.AvgLatency.Nanoseconds()),
		fmt.Sprintf("%d", result.MinLatency.Nanoseconds()),
		fmt.Sprintf("%d", result.MaxLatency.Nanoseconds()),
		fmt.Sprintf("%d", result.P95Latency.Nanoseconds()),
		fmt.Sprintf("%d", result.TotalRequests),
		fmt.Sprintf("%d", result.Errors),
	}

	fmt.Println(strings.Join(headers, ","))
	fmt.Println(strings.Join(values, ","))
}
