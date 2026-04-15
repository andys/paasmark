package ui

import (
	"context"
	"time"

	"github.com/andys/paasmark/benchmark"
	"github.com/andys/paasmark/changesets"
	"github.com/andys/paasmark/ui/templates"

	"github.com/gofiber/fiber/v2"
)

//go:generate qtc -dir=templates

func Setup(app *fiber.App) {
	// Home - benchmark form
	app.Get("/", benchmarkForm)
	app.Post("/benchmark", runBenchmark)
}

func benchmarkForm(c *fiber.Ctx) error {
	return c.Type("html").SendString(templates.BenchmarkForm(changesets.BenchmarkForm{
		Driver:      "pgx",
		Concurrency: "5",
		Duration:    "5",
		QueryType:   "mixed",
		SeedDataMB:  "10",
	}, nil, nil, nil))
}

func runBenchmark(c *fiber.Ctx) error {
	var form changesets.BenchmarkForm
	if err := c.BodyParser(&form); err != nil {
		return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, err))
	}

	cfg, err := form.ToConfig()
	if err != nil {
		return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, err))
	}

	// Run benchmark with timeout (allow extra time for setup, seeding, and cleanup)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration+2*time.Minute+10*time.Second)
	defer cancel()

	// Run CPU benchmark first (10 seconds, 20 threads)
	cpuResult := benchmark.RunCPU(ctx, 10*time.Second, 20)

	// Then run DB benchmark
	dbResult, dbErr := benchmark.Run(ctx, cfg)
	if dbErr != nil {
		return c.Type("html").SendString(templates.BenchmarkForm(form, nil, cpuResult, dbErr))
	}

	return c.Type("html").SendString(templates.BenchmarkForm(form, dbResult, cpuResult, nil))
}
