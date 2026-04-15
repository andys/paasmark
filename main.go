package main

import (
	"fmt"
	"os"

	"github.com/andys/paasmark/cli"
	"github.com/andys/paasmark/server"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Check if running in CLI mode (first argument is "remote")
	if len(os.Args) > 1 && os.Args[0] != "server" {
		// Remove the "remote" argument and run CLI
		if os.Args[1] == "remote" {
			os.Args = append(os.Args[:1], os.Args[2:]...)
			if err := cli.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	// Default: Server mode
	// Setup logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Create server
	app := server.Create()

	// Get port
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	log.Info().Str("port", port).Msg("Starting server")

	// Start server
	if err := server.Listen(app, port); err != nil {
		log.Fatal().Err(err).Msg("Server failed")
	}
}
