package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// BenchmarkRequest matches the API request structure
type BenchmarkRequest struct {
	Driver      string `json:"driver"`
	DSN         string `json:"dsn"`
	Concurrency int    `json:"concurrency"`
	Duration    int    `json:"duration"`
	QueryType   string `json:"query_type"`
	SeedDataMB  int    `json:"seed_data_mb"`
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

// ResultSet holds the benchmark result along with metadata
type ResultSet struct {
	ID        string          `json:"id"`
	Status    BenchmarkStatus `json:"status"`
	Error     string          `json:"error,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	Result    *Result         `json:"result,omitempty"`
	CPUResult *CPUResult      `json:"cpu_result,omitempty"`
}

// Config holds CLI configuration
type Config struct {
	Endpoint    string
	DSN         string
	Concurrency int
	Duration    int
	QueryType   string
	SeedDataMB  int
}

// Run executes the CLI mode
func Run() error {
	cfg := Config{}

	flag.StringVar(&cfg.Endpoint, "endpoint", "", "API endpoint URL (required)")
	flag.StringVar(&cfg.DSN, "dsn", "", "Database connection string (required)")
	flag.IntVar(&cfg.Concurrency, "concurrency", 10, "Number of concurrent workers")
	flag.IntVar(&cfg.Duration, "duration", 30, "Benchmark duration in seconds")
	flag.StringVar(&cfg.QueryType, "query-type", "mixed", "Query type: read, write, or mixed")
	flag.IntVar(&cfg.SeedDataMB, "seed-data-mb", 10, "MB of test data to seed before benchmark")

	flag.Parse()

	// Validate required fields
	if cfg.Endpoint == "" {
		return fmt.Errorf("--endpoint is required")
	}
	if cfg.DSN == "" {
		return fmt.Errorf("--dsn is required")
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
		DSN:         cfg.DSN,
		Concurrency: cfg.Concurrency,
		Duration:    cfg.Duration,
		QueryType:   cfg.QueryType,
		SeedDataMB:  cfg.SeedDataMB,
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
	if r == nil {
		fmt.Fprintln(os.Stderr, "No result data available")
		return
	}

	// Build header and values as single row
	headers := []string{
		"init_duration_ns",
		"total_duration_ns",
		"queries_per_sec",
		"avg_latency_ns",
		"min_latency_ns",
		"max_latency_ns",
		"p95_latency_ns",
		"errors",
	}
	values := []string{
		fmt.Sprintf("%d", r.InitDuration),
		fmt.Sprintf("%d", r.TotalDuration),
		fmt.Sprintf("%.2f", r.QueriesPerSec),
		fmt.Sprintf("%d", r.AvgLatency),
		fmt.Sprintf("%d", r.MinLatency),
		fmt.Sprintf("%d", r.MaxLatency),
		fmt.Sprintf("%d", r.P95Latency),
		fmt.Sprintf("%d", r.Errors),
	}

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

	// CPU/RAM stats
	if rs.CPUResult != nil {
		headers = append(headers,
			"cpu_hashes_per_sec",
			"cpu_total_hashes",
			"cpu_num_cores",
			"available_memory_mb",
		)
		values = append(values,
			fmt.Sprintf("%.2f", rs.CPUResult.HashesPerSec),
			fmt.Sprintf("%d", rs.CPUResult.TotalHashes),
			fmt.Sprintf("%d", rs.CPUResult.NumCPU),
			fmt.Sprintf("%d", rs.CPUResult.AvailableMemoryMB),
		)
	}

	// Output header row
	fmt.Println(strings.Join(headers, ","))
	// Output values row
	fmt.Println(strings.Join(values, ","))
}
