package benchmark

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig holds Redis benchmark configuration
type RedisConfig struct {
	DSN         string
	Concurrency int
	Duration    time.Duration
	SeedDataMB  int // MB of test data to insert at start
}

// RedisResult holds Redis benchmark results
type RedisResult struct {
	InitDuration  time.Duration // Time taken to seed data
	TotalDuration time.Duration
	KeysPerSec    float64
	AvgLatency    time.Duration
	MinLatency    time.Duration
	MaxLatency    time.Duration
	P95Latency    time.Duration
	TotalKeys     int64
	Errors        int64
	ErrorMessages []string
}

// RunRedis executes the Redis benchmark with the given configuration
func RunRedis(ctx context.Context, cfg RedisConfig) (*RedisResult, error) {
	// Parse Redis DSN and create client
	opts, err := redis.ParseURL(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis DSN: %w", err)
	}

	client := redis.NewClient(opts)
	defer client.Close()

	// Test connection with generous timeout
	pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Setup: seed data based on SeedDataMB
	setupTimeout := 60*time.Second + time.Duration(cfg.SeedDataMB)*10*time.Second
	setupCtx, setupCancel := context.WithTimeout(ctx, setupTimeout)
	defer setupCancel()
	initStart := time.Now()
	totalKeys, err := seedRedisData(setupCtx, client, cfg.SeedDataMB)
	if err != nil {
		return nil, fmt.Errorf("failed to seed Redis data: %w", err)
	}
	initDuration := time.Since(initStart)
	defer cleanupRedisData(client, totalKeys)

	// Run benchmark
	result := runRedisBenchmark(ctx, client, cfg, totalKeys)
	result.InitDuration = initDuration
	result.TotalKeys = int64(totalKeys)

	return result, nil
}

// seedRedisData fills Redis with the specified MB of string data using incrementing keys
// Returns the number of keys inserted
func seedRedisData(ctx context.Context, client *redis.Client, targetMB int) (int, error) {
	if targetMB <= 0 {
		// Insert minimal seed data
		targetMB = 1
	}

	// Target bytes
	targetBytes := int64(targetMB) * 1024 * 1024

	// Average value size: 128 bytes (similar to DB benchmark)
	// Key format: "paasmark:bench:XXXXXXXX" = ~24 bytes
	// Total per key: ~152 bytes
	const avgValueSize = 128
	const avgTotalSize = 152
	const numWorkers = 20
	const batchSize = 1000

	totalKeys := int(targetBytes / avgTotalSize)
	keysPerWorker := totalKeys / numWorkers

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var insertedKeys int64

	// Progress reporting goroutine
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				current := atomic.LoadInt64(&insertedKeys)
				fmt.Fprintf(os.Stderr, "Redis seeding progress: %d/%d keys (%.1f%%)\n", current, totalKeys, float64(current)/float64(totalKeys)*100)
			}
		}
	}()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Each worker has its own RNG for thread safety
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))

			// Calculate this worker's key range
			startKey := workerID * keysPerWorker
			endKey := startKey + keysPerWorker
			if workerID == numWorkers-1 {
				endKey = totalKeys // Last worker handles remainder
			}

			// Process in batches using pipeline
			for batchStart := startKey; batchStart < endKey; batchStart += batchSize {
				// Check for cancellation or prior error
				select {
				case <-ctx.Done():
					return
				default:
				}
				mu.Lock()
				if firstErr != nil {
					mu.Unlock()
					return
				}
				mu.Unlock()

				batchEnd := batchStart + batchSize
				if batchEnd > endKey {
					batchEnd = endKey
				}

				err := insertRedisBatch(ctx, client, rng, batchStart, batchEnd)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("worker %d failed: %w", workerID, err)
					}
					mu.Unlock()
					return
				}

				atomic.AddInt64(&insertedKeys, int64(batchEnd-batchStart))
			}
		}(w)
	}

	wg.Wait()
	close(done)

	if firstErr != nil {
		return 0, firstErr
	}

	fmt.Fprintf(os.Stderr, "Seeded %d Redis keys (~%d MB)\n", totalKeys, targetMB)
	return totalKeys, nil
}

// insertRedisBatch inserts a batch of keys using Redis pipeline
func insertRedisBatch(ctx context.Context, client *redis.Client, rng *rand.Rand, startKey, endKey int) error {
	pipe := client.Pipeline()

	for i := startKey; i < endKey; i++ {
		key := fmt.Sprintf("paasmark:bench:%08d", i)
		// Generate value with length 0-255, averaging 128
		valueLen := triangularRandom(rng, 0, 255, 128)
		value := randomString(rng, valueLen, "")
		pipe.Set(ctx, key, value, 0)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// cleanupRedisData removes all benchmark keys from Redis
func cleanupRedisData(client *redis.Client, totalKeys int) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Delete keys in batches using pipeline
	const batchSize = 1000
	for start := 0; start < totalKeys; start += batchSize {
		end := start + batchSize
		if end > totalKeys {
			end = totalKeys
		}

		pipe := client.Pipeline()
		for i := start; i < end; i++ {
			key := fmt.Sprintf("paasmark:bench:%08d", i)
			pipe.Del(ctx, key)
		}
		pipe.Exec(ctx)
	}
}

// runRedisBenchmark executes the actual benchmark (concurrent reads)
func runRedisBenchmark(ctx context.Context, client *redis.Client, cfg RedisConfig, totalKeys int) *RedisResult {
	var (
		totalReads int64
		totalErrs  int64
		latencies  []time.Duration
		errorMsgs  []string
		mu         sync.Mutex
		wg         sync.WaitGroup
		stopping   bool
	)

	// Create a context with timeout
	benchCtx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	// Start workers
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Each worker has its own RNG
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))

			for {
				select {
				case <-benchCtx.Done():
					return
				default:
					// Read a random key
					keyIndex := rng.Intn(totalKeys)
					key := fmt.Sprintf("paasmark:bench:%08d", keyIndex)

					start := time.Now()
					_, err := client.Get(benchCtx, key).Result()
					elapsed := time.Since(start)

					mu.Lock()
					// Check if benchmark is stopping - if so, ignore errors
					if benchCtx.Err() != nil {
						stopping = true
					}
					if !stopping {
						totalReads++
						latencies = append(latencies, elapsed)
						if err != nil && err != redis.Nil {
							totalErrs++
							if len(errorMsgs) < 10 {
								errorMsgs = append(errorMsgs, err.Error())
							}
						}
					}
					mu.Unlock()
				}
			}
		}(i)
	}

	wg.Wait()

	// Calculate statistics
	result := &RedisResult{
		TotalDuration: cfg.Duration,
		Errors:        totalErrs,
		ErrorMessages: errorMsgs,
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
		result.KeysPerSec = float64(totalReads) / cfg.Duration.Seconds()

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

	return result
}
