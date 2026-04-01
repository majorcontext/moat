package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/majorcontext/moat/internal/gatekeeper"
)

func main() {
	configPath := flag.String("config", "", "path to gatekeeper.yaml")
	flag.Parse()

	if *configPath == "" {
		*configPath = os.Getenv("GATEKEEPER_CONFIG")
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "error: --config or GATEKEEPER_CONFIG required")
		os.Exit(1)
	}

	cfg, err := gatekeeper.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	srv, err := gatekeeper.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating server: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
