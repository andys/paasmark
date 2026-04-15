package server

import (
	"time"

	"github.com/andys/paasmark/ui"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

// Create creates a new Fiber app with optional prefork support.
// preforkCount > 0 enables Fiber's prefork mode which forks the process
// into multiple child processes to utilize all CPU cores.
func Create(preforkCount int) *fiber.App {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	log.Info().Msg("server: started")

	config := fiber.Config{
		CaseSensitive: true,
		ReadTimeout:   5 * time.Minute,
		WriteTimeout:  5 * time.Minute,
		Concurrency:   100,
	}

	// Enable prefork if requested
	if preforkCount > 0 {
		config.Prefork = true
	}

	app := fiber.New(config)

	// Middlewares
	app.Use(logger.New(logger.Config{
		Next: func(c *fiber.Ctx) bool {
			return c.Path() == "/ping"
		},
	}))
	app.Use(recover.New())
	app.Use(compress.New(compress.Config{
		Level: compress.LevelBestSpeed,
	}))

	// API routes
	SetupAPI(app)

	// UI routes
	ui.Setup(app)

	return app
}

func Listen(app *fiber.App, port string) error {
	return app.Listen(":" + port)
}
