package server

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/andys/paasmark/benchmark"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// detectDriver auto-detects the database driver from DSN format
func detectDriver(dsn string) string {
	dsn = strings.ToLower(dsn)
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return "pgx"
	}
	// MySQL DSNs typically look like: user:password@tcp(host:port)/dbname
	// or user:password@host:port/dbname
	if strings.Contains(dsn, "@tcp(") || strings.Contains(dsn, "@unix(") {
		return "mysql"
	}
	// Also check for mysql:// prefix (less common but possible)
	if strings.HasPrefix(dsn, "mysql://") {
		return "mysql"
	}
	return ""
}

// BenchmarkRequest contains parameters for launching a benchmark
type BenchmarkRequest struct {
	Driver      string `json:"driver"`       // "pgx" or "mysql"
	DSN         string `json:"dsn"`          // Database connection string
	Concurrency int    `json:"concurrency"`  // Number of concurrent workers
	Duration    int    `json:"duration"`     // Duration in seconds
	QueryType   string `json:"query_type"`   // "read", "write", or "mixed"
	SeedDataMB  int    `json:"seed_data_mb"` // MB of test data to insert
}

// BenchmarkStatus represents the current state of a benchmark run
type BenchmarkStatus string

const (
	StatusPending  BenchmarkStatus = "pending"
	StatusRunning  BenchmarkStatus = "running"
	StatusComplete BenchmarkStatus = "complete"
	StatusFailed   BenchmarkStatus = "failed"
)

// ResultSet holds the benchmark result along with metadata
type ResultSet struct {
	ID        string               `json:"id"`
	Status    BenchmarkStatus      `json:"status"`
	Error     string               `json:"error,omitempty"`
	CreatedAt time.Time            `json:"created_at"`
	Result    *benchmark.Result    `json:"result,omitempty"`
	CPUResult *benchmark.CPUResult `json:"cpu_result,omitempty"`
}

// resultStore holds all benchmark results in memory
var resultStore = struct {
	sync.RWMutex
	results map[string]*ResultSet
}{
	results: make(map[string]*ResultSet),
}

// SetupAPI registers the API routes
func SetupAPI(app *fiber.App) {
	api := app.Group("/api")
	api.Post("/benchmark", handleLaunchBenchmark)
	api.Get("/benchmark/:id", handleGetBenchmark)
}

// handleLaunchBenchmark starts a new benchmark run and returns the ID immediately
func handleLaunchBenchmark(c *fiber.Ctx) error {
	var req BenchmarkRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body: " + err.Error(),
		})
	}

	// Validate required fields
	if req.DSN == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "dsn is required",
		})
	}

	// Auto-detect driver from DSN if not specified
	if req.Driver == "" {
		req.Driver = detectDriver(req.DSN)
		if req.Driver == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "unable to detect driver from DSN, please specify driver",
			})
		}
	}

	// Set defaults
	if req.Concurrency <= 0 {
		req.Concurrency = 10
	}
	if req.Duration <= 0 {
		req.Duration = 30
	}
	if req.QueryType == "" {
		req.QueryType = "mixed"
	}

	// Create result set with unique ID
	id := uuid.New().String()
	rs := &ResultSet{
		ID:        id,
		Status:    StatusPending,
		CreatedAt: time.Now(),
	}

	resultStore.Lock()
	resultStore.results[id] = rs
	resultStore.Unlock()

	// Launch benchmark in background
	go runBenchmarkAsync(id, req)

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"id":     id,
		"status": StatusPending,
	})
}

// runBenchmarkAsync executes the benchmark and updates the result set
func runBenchmarkAsync(id string, req BenchmarkRequest) {
	resultStore.Lock()
	rs := resultStore.results[id]
	rs.Status = StatusRunning
	resultStore.Unlock()

	cfg := benchmark.Config{
		Driver:      req.Driver,
		DSN:         req.DSN,
		Concurrency: req.Concurrency,
		Duration:    time.Duration(req.Duration) * time.Second,
		QueryType:   req.QueryType,
		SeedDataMB:  req.SeedDataMB,
	}

	// Run CPU benchmark (10 seconds, 20 threads)
	cpuResult := benchmark.RunCPU(context.Background(), 10*time.Second, 20)

	result, err := benchmark.Run(context.Background(), cfg)

	resultStore.Lock()
	defer resultStore.Unlock()

	if err != nil {
		rs.Status = StatusFailed
		rs.Error = err.Error()
	} else {
		rs.Status = StatusComplete
		rs.Result = result
		rs.CPUResult = cpuResult
	}
}

// handleGetBenchmark returns the current status and result of a benchmark run
func handleGetBenchmark(c *fiber.Ctx) error {
	id := c.Params("id")

	resultStore.RLock()
	rs, exists := resultStore.results[id]
	resultStore.RUnlock()

	if !exists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "benchmark not found",
		})
	}

	return c.JSON(rs)
}
