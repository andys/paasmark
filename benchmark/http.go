package benchmark

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// HTTPConfig holds HTTP benchmark configuration
type HTTPConfig struct {
	URL         string        // Base URL to benchmark
	Concurrency int           // Number of concurrent workers
	Duration    time.Duration // How long to run the benchmark
}

// HTTPResult holds HTTP benchmark results
type HTTPResult struct {
	TotalDuration  time.Duration
	TotalRequests  int64
	RequestsPerSec float64
	AvgLatency     time.Duration
	MinLatency     time.Duration
	MaxLatency     time.Duration
	P95Latency     time.Duration
	Errors         int64
	ErrorMessages  []string
	SuccessfulReqs int64
	BytesReceived  int64
}

// RunHTTP executes an HTTP benchmark against the given URL
// It appends /ping to the URL if not already present
func RunHTTP(ctx context.Context, cfg HTTPConfig) (*HTTPResult, error) {
	// Ensure URL ends with /ping
	url := strings.TrimSuffix(cfg.URL, "/")
	if !strings.HasSuffix(url, "/ping") {
		url = url + "/ping"
	}

	// Create HTTP client with reasonable timeouts
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        cfg.Concurrency * 2,
			MaxIdleConnsPerHost: cfg.Concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	defer client.CloseIdleConnections()

	// Test the endpoint first
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", url, err)
	}
	resp.Body.Close()

	var (
		totalRequests  int64
		totalErrors    int64
		successfulReqs int64
		bytesReceived  int64
		latencies      []time.Duration
		errorMsgs      []string
		mu             sync.Mutex
		wg             sync.WaitGroup
		stopping       atomic.Bool
	)

	// Create a context with timeout
	benchCtx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	// Start workers
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				select {
				case <-benchCtx.Done():
					return
				default:
					start := time.Now()
					bytes, err := makeRequest(benchCtx, client, url)
					elapsed := time.Since(start)

					// Check if we're stopping
					if stopping.Load() || benchCtx.Err() != nil {
						return
					}

					mu.Lock()
					totalRequests++
					latencies = append(latencies, elapsed)
					if err != nil {
						totalErrors++
						if len(errorMsgs) < 10 {
							errorMsgs = append(errorMsgs, err.Error())
						}
					} else {
						successfulReqs++
						bytesReceived += int64(bytes)
					}
					mu.Unlock()
				}
			}
		}(i)
	}

	// Wait for context to finish
	<-benchCtx.Done()
	stopping.Store(true)
	wg.Wait()

	// Calculate statistics
	result := &HTTPResult{
		TotalDuration:  cfg.Duration,
		TotalRequests:  totalRequests,
		Errors:         totalErrors,
		ErrorMessages:  errorMsgs,
		SuccessfulReqs: successfulReqs,
		BytesReceived:  bytesReceived,
	}

	if len(latencies) > 0 {
		var totalLatency time.Duration
		result.MinLatency = latencies[0]
		result.MaxLatency = latencies[0]

		for _, l := range latencies {
			totalLatency += l
			if l < result.MinLatency {
				result.MinLatency = l
			}
			if l > result.MaxLatency {
				result.MaxLatency = l
			}
		}

		result.AvgLatency = totalLatency / time.Duration(len(latencies))
		result.RequestsPerSec = float64(totalRequests) / cfg.Duration.Seconds()

		// Calculate 95th percentile latency
		sortedLatencies := make([]time.Duration, len(latencies))
		copy(sortedLatencies, latencies)
		sort.Slice(sortedLatencies, func(i, j int) bool {
			return sortedLatencies[i] < sortedLatencies[j]
		})
		p95Index := int(float64(len(sortedLatencies)) * 0.95)
		if p95Index >= len(sortedLatencies) {
			p95Index = len(sortedLatencies) - 1
		}
		result.P95Latency = sortedLatencies[p95Index]
	}

	return result, nil
}

// makeRequest performs a single HTTP GET request and returns the number of bytes read
func makeRequest(ctx context.Context, client *http.Client, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// Read and discard body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	if resp.StatusCode != http.StatusOK {
		return len(body), fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return len(body), nil
}
