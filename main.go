package main

import (
	"flag"
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
	// Parse server flags
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Paasmark - PaaS Benchmarking Tool

Usage:
  paasmark [server flags]    Start the benchmark server (default)
  paasmark remote [flags]    Run benchmarks from CLI

Server Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Environment Variables:
  PORT    Server port (default: 3000)

For CLI help: paasmark remote -h
`)
	}

	prefork := flag.Int("prefork", 0, "Number of child processes to fork (0 = disabled)")
	flag.Parse()

	// Setup logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Create server with prefork option
	app := server.Create(*prefork)

	// Get port
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	if *prefork > 0 {
		log.Info().Str("port", port).Int("prefork", *prefork).Msg("Starting server with prefork")
	} else {
		log.Info().Str("port", port).Msg("Starting server")
	}

	// Start server
	if err := server.Listen(app, port); err != nil {
		log.Fatal().Err(err).Msg("Server failed")
	}
}
