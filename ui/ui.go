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
		Driver:        "pgx",
		Concurrency:   "5",
		Duration:      "5",
		QueryType:     "mixed",
		SeedDataMB:    "10",
		BenchmarkType: "cpu",
	}, nil, nil, nil, nil, nil))
}

func runBenchmark(c *fiber.Ctx) error {
	var form changesets.BenchmarkForm
	if err := c.BodyParser(&form); err != nil {
		return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, nil, nil, err))
	}

	// Default benchmark type if not set
	if form.BenchmarkType == "" {
		form.BenchmarkType = "cpu"
	}

	var cpuResult *benchmark.CPUResult
	var dbResult *benchmark.Result
	var redisResult *benchmark.RedisResult
	var httpResult *benchmark.HTTPResult

	// Run CPU benchmark if requested (10 seconds, 20 threads)
	if form.BenchmarkType == "cpu" {
		cfg, err := form.ToConfig()
		if err != nil {
			return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, nil, nil, err))
		}
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration+2*time.Minute+10*time.Second)
		defer cancel()
		cpuResult = benchmark.RunCPU(ctx, 10*time.Second, 20)
	}

	// Run DB benchmark if requested
	if form.BenchmarkType == "db" {
		cfg, err := form.ToConfig()
		if err != nil {
			return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, nil, nil, err))
		}
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration+2*time.Minute+10*time.Second)
		defer cancel()
		dbResult, err = benchmark.Run(ctx, cfg)
		if err != nil {
			return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, nil, nil, err))
		}
	}

	// Run Redis benchmark if requested
	if form.BenchmarkType == "redis" {
		redisCfg, err := form.ToRedisConfig()
		if err != nil {
			return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, nil, nil, err))
		}
		ctx, cancel := context.WithTimeout(context.Background(), redisCfg.Duration+2*time.Minute+10*time.Second)
		defer cancel()
		redisResult, err = benchmark.RunRedis(ctx, redisCfg)
		if err != nil {
			return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, nil, nil, err))
		}
	}

	// Run HTTP benchmark if requested
	if form.BenchmarkType == "http" {
		httpCfg, err := form.ToHTTPConfig()
		if err != nil {
			return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, nil, nil, err))
		}
		ctx, cancel := context.WithTimeout(context.Background(), httpCfg.Duration+10*time.Second)
		defer cancel()
		httpResult, err = benchmark.RunHTTP(ctx, httpCfg)
		if err != nil {
			return c.Type("html").SendString(templates.BenchmarkForm(form, nil, nil, nil, nil, err))
		}
	}

	return c.Type("html").SendString(templates.BenchmarkForm(form, dbResult, cpuResult, redisResult, httpResult, nil))
}
