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

func Create() *fiber.App {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	log.Info().Msg("server: started")

	app := fiber.New(fiber.Config{
		CaseSensitive: true,
		ReadTimeout:   5 * time.Minute,
		WriteTimeout:  5 * time.Minute,
		Concurrency:   100,
	})

	// Middlewares
	app.Use(logger.New())
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
