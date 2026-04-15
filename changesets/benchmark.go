package changesets

import (
	"strconv"
	"time"

	"github.com/andys/paasmark/benchmark"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// BenchmarkForm holds form data for benchmark configuration
type BenchmarkForm struct {
	Driver      string `form:"driver"`
	DSN         string `form:"dsn"`
	Concurrency string `form:"concurrency"`
	Duration    string `form:"duration"`
	QueryType   string `form:"query_type"`
	SeedDataMB  string `form:"seed_data_mb"`
}

// Validate validates the benchmark form data
func (f BenchmarkForm) Validate() error {
	return validation.ValidateStruct(&f,
		validation.Field(&f.Driver, validation.Required, validation.In("pgx", "mysql")),
		validation.Field(&f.DSN, validation.Required),
		validation.Field(&f.Concurrency, validation.Required),
		validation.Field(&f.Duration, validation.Required),
		validation.Field(&f.QueryType, validation.Required, validation.In("read", "write", "mixed")),
	)
}

// ToConfig converts form data to benchmark.Config
func (f BenchmarkForm) ToConfig() (benchmark.Config, error) {
	if err := f.Validate(); err != nil {
		return benchmark.Config{}, err
	}

	concurrency, err := strconv.Atoi(f.Concurrency)
	if err != nil || concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 100 {
		concurrency = 100
	}

	duration, err := strconv.Atoi(f.Duration)
	if err != nil || duration < 1 {
		duration = 5
	}
	if duration > 60 {
		duration = 60
	}

	seedDataMB, _ := strconv.Atoi(f.SeedDataMB)
	if seedDataMB < 0 {
		seedDataMB = 0
	}
	if seedDataMB > 1000 {
		seedDataMB = 1000
	}

	return benchmark.Config{
		Driver:      f.Driver,
		DSN:         f.DSN,
		Concurrency: concurrency,
		Duration:    time.Duration(duration) * time.Second,
		QueryType:   f.QueryType,
		SeedDataMB:  seedDataMB,
	}, nil
}
