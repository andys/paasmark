package benchmark

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Config holds benchmark configuration
type Config struct {
	Driver      string // "postgres" or "mysql"
	DSN         string
	Concurrency int
	Duration    time.Duration
	QueryType   string // "read", "write", "mixed"
	SeedDataMB  int    // MB of test data to insert at start
	rowCount    int    // internal: number of rows seeded (for UPDATE queries)
}

// QueryStats holds statistics for a category of queries
type QueryStats struct {
	Count         int64
	QueriesPerSec float64
	AvgLatency    time.Duration
	MinLatency    time.Duration
	MaxLatency    time.Duration
	P95Latency    time.Duration
}

// Result holds benchmark results
type Result struct {
	InitDuration  time.Duration // Time taken to initialize the database (setup table and seed data)
	TotalDuration time.Duration
	QueriesPerSec float64
	AvgLatency    time.Duration
	MinLatency    time.Duration
	MaxLatency    time.Duration
	P95Latency    time.Duration
	Errors        int64
	ErrorMessages []string
	// Separate stats for read and write queries
	ReadStats  *QueryStats
	WriteStats *QueryStats
	// Sequential row reading benchmark results
	SequentialResult *SequentialRowResult
}

// Run executes the benchmark with the given configuration
func Run(ctx context.Context, cfg Config) (*Result, error) {
	// Open database connection
	db, err := sql.Open(cfg.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	// Test connection with generous timeout
	pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(cfg.Concurrency * 2)
	db.SetMaxIdleConns(cfg.Concurrency)

	// Setup test table with generous timeout (add extra time for seeding large datasets)
	setupTimeout := 60*time.Second + time.Duration(cfg.SeedDataMB)*30*time.Second
	setupCtx, setupCancel := context.WithTimeout(ctx, setupTimeout)
	defer setupCancel()
	initStart := time.Now()
	rowCount, err := setupTable(setupCtx, db, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to setup table: %w", err)
	}
	cfg.rowCount = rowCount
	initDuration := time.Since(initStart)
	defer cleanupTable(db)

	// Run benchmark
	result := runBenchmark(ctx, db, cfg)
	result.InitDuration = initDuration

	// Run sequential read benchmark after main benchmark completes
	seqCtx, seqCancel := context.WithTimeout(ctx, 60*time.Second)
	defer seqCancel()
	seqResult, seqErr := RunSequentialRead(seqCtx, db, cfg.Driver)
	if seqErr != nil {
		// Log but don't fail the entire benchmark
		fmt.Fprintf(os.Stderr, "Sequential read benchmark failed: %v\n", seqErr)
	} else {
		result.SequentialResult = seqResult
	}

	return result, nil
}

func setupTable(ctx context.Context, db *sql.DB, cfg Config) (int, error) {
	// Drop and create test table
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS benchmark_test")

	var createSQL string
	if cfg.Driver == "postgres" || cfg.Driver == "pgx" {
		createSQL = `CREATE TABLE benchmark_test (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255),
			value INTEGER,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`
	} else {
		createSQL = `CREATE TABLE benchmark_test (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255),
			value INTEGER,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`
	}

	if _, err := db.ExecContext(ctx, createSQL); err != nil {
		return 0, err
	}

	// Seed data based on SeedDataMB
	var rowCount int
	if cfg.SeedDataMB > 0 {
		var err error
		rowCount, err = seedData(ctx, db, cfg.Driver, cfg.SeedDataMB)
		if err != nil {
			return 0, fmt.Errorf("failed to seed data: %w", err)
		}
	} else {
		// Insert minimal seed data for read tests
		rowCount = 100
		for i := 0; i < rowCount; i++ {
			_, err := db.ExecContext(ctx, "INSERT INTO benchmark_test (name, value) VALUES ($1, $2)",
				fmt.Sprintf("test_%d", i), i)
			if err != nil {
				// Try MySQL syntax
				_, err = db.ExecContext(ctx, "INSERT INTO benchmark_test (name, value) VALUES (?, ?)",
					fmt.Sprintf("test_%d", i), i)
				if err != nil {
					return 0, err
				}
			}
		}
	}

	return rowCount, nil
}

// seedData inserts enough rows to meet the MB requirement using parallel workers and batched transactions.
// The name field varies from 0 to 255 characters with an average of 128 characters.
// Returns the number of rows inserted.
func seedData(ctx context.Context, db *sql.DB, driver string, targetMB int) (int, error) {
	// Average row size estimation:
	// - name: average 128 bytes
	// - value: 4 bytes (int)
	// - id: 4-8 bytes
	// - created_at: 8 bytes
	// - row overhead: ~20-30 bytes
	// Total average: ~170 bytes per row
	const avgRowSize = 170
	const numWorkers = 20
	const batchSize = 1000

	targetBytes := int64(targetMB) * 1024 * 1024
	totalRows := int(targetBytes / avgRowSize)
	rowsPerWorker := totalRows / numWorkers

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var insertedRows int64

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
				mu.Lock()
				current := insertedRows
				mu.Unlock()
				fmt.Fprintf(os.Stderr, "Seeding progress: %d/%d rows (%.1f%%)\n", current, totalRows, float64(current)/float64(totalRows)*100)
			}
		}
	}()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Each worker has its own RNG for thread safety
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))
			const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

			// Calculate this worker's row range
			startRow := workerID * rowsPerWorker
			endRow := startRow + rowsPerWorker
			if workerID == numWorkers-1 {
				endRow = totalRows // Last worker handles remainder
			}

			// Process in batches with transactions
			for batchStart := startRow; batchStart < endRow; batchStart += batchSize {
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
				if batchEnd > endRow {
					batchEnd = endRow
				}

				err := insertBatch(ctx, db, driver, rng, charset, batchEnd-batchStart)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("worker %d failed: %w", workerID, err)
					}
					mu.Unlock()
					return
				}

				mu.Lock()
				insertedRows += int64(batchEnd - batchStart)
				mu.Unlock()
			}
		}(w)
	}

	wg.Wait()
	close(done)

	if firstErr != nil {
		return 0, firstErr
	}

	fmt.Fprintf(os.Stderr, "Seeded %d rows (~%d MB)\n", totalRows, targetMB)
	return totalRows, nil
}

// insertBatch inserts a batch of rows within a single transaction
func insertBatch(ctx context.Context, db *sql.DB, driver string, rng *rand.Rand, charset string, count int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Prepare the statement within the transaction
	var insertSQL string
	if driver == "postgres" || driver == "pgx" {
		insertSQL = "INSERT INTO benchmark_test (name, value) VALUES ($1, $2)"
	} else {
		insertSQL = "INSERT INTO benchmark_test (name, value) VALUES (?, ?)"
	}

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for i := 0; i < count; i++ {
		// Generate name with length 0-255, averaging 128
		nameLen := triangularRandom(rng, 0, 255, 128)
		name := randomString(rng, nameLen, charset)
		value := rng.Intn(1000000)

		_, err := stmt.ExecContext(ctx, name, value)
		if err != nil {
			return fmt.Errorf("failed to insert row: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// triangularRandom generates a random number with triangular distribution
func triangularRandom(rng *rand.Rand, min, max, mode int) int {
	u := rng.Float64()
	fc := float64(mode-min) / float64(max-min)
	if u < fc {
		return min + int(float64(max-min)*float64(u*fc)*0.5+0.5)
	}
	return max - int(float64(max-min)*float64((1-u)*(1-fc))*0.5+0.5)
}

// pregenString is a long pre-generated string used for fast random substring extraction.
// It's 512 characters long so we can extract substrings of up to 255 chars from any starting position.
var pregenString = func() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 512)
	for i := range b {
		b[i] = charset[i%len(charset)]
	}
	return string(b)
}()

// randomString returns a random substring of the specified length from the pre-generated string.
// This is much faster than generating random characters one by one.
func randomString(rng *rand.Rand, length int, charset string) string {
	if length == 0 {
		return ""
	}
	// Pick a random starting position that allows extracting 'length' characters
	maxStart := len(pregenString) - length
	start := rng.Intn(maxStart + 1)
	return pregenString[start : start+length]
}

func cleanupTable(db *sql.DB) {
	// Use a fresh context for cleanup since the benchmark context may be expired
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS benchmark_test")
}

func runBenchmark(ctx context.Context, db *sql.DB, cfg Config) *Result {
	var (
		totalQueries   int64
		totalErrors    int64
		latencies      []time.Duration
		readLatencies  []time.Duration
		writeLatencies []time.Duration
		errorMsgs      []string
		mu             sync.Mutex
		wg             sync.WaitGroup
		stopping       bool // Flag to ignore errors after benchmark duration ends
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
					isWrite, err := runQueryWithType(benchCtx, db, cfg)
					elapsed := time.Since(start)

					mu.Lock()
					// Check if benchmark is stopping - if so, ignore errors
					if benchCtx.Err() != nil {
						stopping = true
					}
					if !stopping {
						totalQueries++
						latencies = append(latencies, elapsed)
						if isWrite {
							writeLatencies = append(writeLatencies, elapsed)
						} else {
							readLatencies = append(readLatencies, elapsed)
						}
						if err != nil {
							totalErrors++
							if len(errorMsgs) < 10 {
								errorMsgs = append(errorMsgs, err.Error())
							}
							fmt.Fprintf(os.Stderr, "Benchmark error: %v\nStack trace:\n%s\n", err, debug.Stack())
						}
					}
					mu.Unlock()
				}
			}
		}(i)
	}

	wg.Wait()

	// Calculate statistics
	result := &Result{
		TotalDuration: cfg.Duration,
		Errors:        totalErrors,
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
		result.QueriesPerSec = float64(totalQueries) / cfg.Duration.Seconds()

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

	// Calculate separate read statistics
	if len(readLatencies) > 0 {
		result.ReadStats = calculateQueryStats(readLatencies, cfg.Duration)
	}

	// Calculate separate write statistics
	if len(writeLatencies) > 0 {
		result.WriteStats = calculateQueryStats(writeLatencies, cfg.Duration)
	}

	return result
}

// calculateQueryStats computes statistics for a set of latencies
func calculateQueryStats(latencies []time.Duration, duration time.Duration) *QueryStats {
	stats := &QueryStats{
		Count:      int64(len(latencies)),
		MinLatency: latencies[0],
		MaxLatency: latencies[0],
	}

	var totalLatency time.Duration
	for _, l := range latencies {
		totalLatency += l
		if l < stats.MinLatency {
			stats.MinLatency = l
		}
		if l > stats.MaxLatency {
			stats.MaxLatency = l
		}
	}

	stats.AvgLatency = totalLatency / time.Duration(len(latencies))
	stats.QueriesPerSec = float64(len(latencies)) / duration.Seconds()

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
	stats.P95Latency = sortedLatencies[p95Index]

	return stats
}

// runQueryWithType executes a query and returns whether it was a write operation
func runQueryWithType(ctx context.Context, db *sql.DB, cfg Config) (isWrite bool, err error) {
	switch cfg.QueryType {
	case "write":
		return true, runWriteQuery(ctx, db, cfg.Driver, cfg.rowCount)
	case "mixed":
		// 85% reads, 15% writes
		if time.Now().UnixNano()%20 < 17 {
			return false, runReadQuery(ctx, db, cfg.Driver)
		}
		return true, runWriteQuery(ctx, db, cfg.Driver, cfg.rowCount)
	default: // "read"
		return false, runReadQuery(ctx, db, cfg.Driver)
	}
}

func runReadQuery(ctx context.Context, db *sql.DB, driver string) error {
	var placeholder string
	if driver == "postgres" || driver == "pgx" {
		placeholder = "$1"
	} else {
		placeholder = "?"
	}

	rows, err := db.QueryContext(ctx,
		fmt.Sprintf("SELECT id, name, value FROM benchmark_test WHERE value > %s LIMIT 10", placeholder),
		50)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var name string
		var value int
		if err := rows.Scan(&id, &name, &value); err != nil {
			return err
		}
	}
	return rows.Err()
}

func runWriteQuery(ctx context.Context, db *sql.DB, driver string, rowCount int) error {
	var placeholder1, placeholder2 string
	if driver == "postgres" || driver == "pgx" {
		placeholder1, placeholder2 = "$1", "$2"
	} else {
		placeholder1, placeholder2 = "?", "?"
	}

	// Generate a random name similar to seed data (0-255 chars)
	// Use pre-generated string for efficiency
	nameLen := rand.Intn(256) // 0-255
	maxStart := len(pregenString) - nameLen
	start := rand.Intn(maxStart + 1)
	name := pregenString[start : start+nameLen]

	// Update a random existing row by ID
	randomID := rand.Intn(rowCount) + 1 // IDs are 1-indexed

	_, err := db.ExecContext(ctx,
		fmt.Sprintf("UPDATE benchmark_test SET name = %s WHERE id = %s", placeholder1, placeholder2),
		name,
		randomID)
	return err
}

// GetDriverName returns the display name for the driver
func GetDriverName(driver string) string {
	switch driver {
	case "pgx":
		return "PostgreSQL"
	case "mysql":
		return "MySQL"
	default:
		return driver
	}
}

// SequentialRowResult holds results for sequential row reading benchmark
type SequentialRowResult struct {
	Duration     time.Duration
	TotalRows    int64
	RowsPerSec   float64
	TotalNameLen int64 // Sum of all name column lengths
}

// CPUResult holds CPU benchmark results
type CPUResult struct {
	Duration          time.Duration
	TotalHashes       int64
	HashesPerSec      float64
	ThreadCount       int
	NumCPU            int   // Number of CPU cores available
	AvailableMemoryMB int64 // Available memory in MB (smaller of CommitLimit or MemTotal)
}

// RunCPU executes a CPU benchmark using SHA256 hashing
// It runs for the specified duration across the given number of threads
func RunCPU(ctx context.Context, duration time.Duration, threads int) *CPUResult {
	var totalHashes atomic.Int64
	var wg sync.WaitGroup

	benchCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	// Start worker goroutines
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Each worker hashes its own data
			data := make([]byte, 64)
			for j := range data {
				data[j] = byte(workerID ^ j)
			}

			var localCount int64
			hash := data

			for {
				select {
				case <-benchCtx.Done():
					totalHashes.Add(localCount)
					return
				default:
					// Chain SHA256 hashes
					sum := sha256.Sum256(hash)
					hash = sum[:]
					localCount++

					// Periodically check context (every 1000 iterations)
					if localCount%1000 == 0 {
						select {
						case <-benchCtx.Done():
							totalHashes.Add(localCount)
							return
						default:
						}
					}
				}
			}
		}(i)
	}

	wg.Wait()

	hashes := totalHashes.Load()
	return &CPUResult{
		Duration:          duration,
		TotalHashes:       hashes,
		HashesPerSec:      float64(hashes) / duration.Seconds(),
		ThreadCount:       threads,
		NumCPU:            runtime.NumCPU(),
		AvailableMemoryMB: getAvailableMemoryMB(),
	}
}

// getAvailableMemoryMB reads /proc/meminfo and returns the smaller of CommitLimit or MemTotal in MB.
// Returns 0 if unable to read memory info.
func getAvailableMemoryMB() int64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()

	var memTotalKB, commitLimitKB int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			memTotalKB = parseMemInfoValue(line)
		} else if strings.HasPrefix(line, "CommitLimit:") {
			commitLimitKB = parseMemInfoValue(line)
		}
		// Stop early if we have both values
		if memTotalKB > 0 && commitLimitKB > 0 {
			break
		}
	}

	// Return the smaller of the two (converted to MB)
	if memTotalKB == 0 {
		return commitLimitKB / 1024
	}
	if commitLimitKB == 0 {
		return memTotalKB / 1024
	}
	if memTotalKB < commitLimitKB {
		return memTotalKB / 1024
	}
	return commitLimitKB / 1024
}

// parseMemInfoValue extracts the numeric value (in kB) from a /proc/meminfo line
// Format: "MemTotal:       16384000 kB"
func parseMemInfoValue(line string) int64 {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	val, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0
	}
	return val
}

// RunSequentialRead runs a sequential table scan benchmark that reads all rows
// and sums the length of all name columns. This tests sequential I/O performance.
func RunSequentialRead(ctx context.Context, db *sql.DB, driver string) (*SequentialRowResult, error) {
	start := time.Now()

	// Run a query that reads all rows and sums the length of name column
	var query string
	if driver == "postgres" || driver == "pgx" {
		query = "SELECT SUM(LENGTH(name)) FROM benchmark_test"
	} else {
		query = "SELECT SUM(CHAR_LENGTH(name)) FROM benchmark_test"
	}

	var totalNameLen sql.NullInt64
	err := db.QueryRowContext(ctx, query).Scan(&totalNameLen)
	if err != nil {
		return nil, fmt.Errorf("sequential read failed: %w", err)
	}

	// Get the row count
	var rowCount int64
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM benchmark_test").Scan(&rowCount)
	if err != nil {
		return nil, fmt.Errorf("row count failed: %w", err)
	}

	elapsed := time.Since(start)

	return &SequentialRowResult{
		Duration:     elapsed,
		TotalRows:    rowCount,
		RowsPerSec:   float64(rowCount) / elapsed.Seconds(),
		TotalNameLen: totalNameLen.Int64,
	}, nil
}
