package changesets

import (
	"strconv"
	"time"

	"github.com/andys/paasmark/benchmark"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// BenchmarkForm holds form data for benchmark configuration
type BenchmarkForm struct {
	Driver        string `form:"driver"`
	DSN           string `form:"dsn"`
	RedisDSN      string `form:"redis_dsn"`
	HTTPURL       string `form:"http_url"`
	Concurrency   string `form:"concurrency"`
	Duration      string `form:"duration"`
	QueryType     string `form:"query_type"`
	SeedDataMB    string `form:"seed_data_mb"`
	BenchmarkType string `form:"benchmark_type"`
}

// Validate validates the benchmark form data
func (f BenchmarkForm) Validate() error {
	// DSN is only required for db benchmark type
	dsnRequired := f.BenchmarkType == "db"
	// Redis DSN is only required for redis benchmark type
	redisDsnRequired := f.BenchmarkType == "redis"
	// HTTP URL is only required for http benchmark type
	httpURLRequired := f.BenchmarkType == "http"

	return validation.ValidateStruct(&f,
		validation.Field(&f.Driver, validation.Required, validation.In("pgx", "mysql")),
		validation.Field(&f.DSN, validation.When(dsnRequired, validation.Required)),
		validation.Field(&f.RedisDSN, validation.When(redisDsnRequired, validation.Required)),
		validation.Field(&f.HTTPURL, validation.When(httpURLRequired, validation.Required)),
		validation.Field(&f.Concurrency, validation.Required),
		validation.Field(&f.Duration, validation.Required),
		validation.Field(&f.QueryType, validation.Required, validation.In("read", "write", "mixed")),
		validation.Field(&f.BenchmarkType, validation.Required, validation.In("cpu", "db", "redis", "http")),
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

// ToRedisConfig converts form data to benchmark.RedisConfig
func (f BenchmarkForm) ToRedisConfig() (benchmark.RedisConfig, error) {
	if err := f.Validate(); err != nil {
		return benchmark.RedisConfig{}, err
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

	return benchmark.RedisConfig{
		DSN:         f.RedisDSN,
		Concurrency: concurrency,
		Duration:    time.Duration(duration) * time.Second,
		SeedDataMB:  seedDataMB,
	}, nil
}

// ToHTTPConfig converts form data to benchmark.HTTPConfig
func (f BenchmarkForm) ToHTTPConfig() (benchmark.HTTPConfig, error) {
	if err := f.Validate(); err != nil {
		return benchmark.HTTPConfig{}, err
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

	return benchmark.HTTPConfig{
		URL:         f.HTTPURL,
		Concurrency: concurrency,
		Duration:    time.Duration(duration) * time.Second,
	}, nil
}
