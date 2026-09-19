package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/destafajri/smart-routing/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(cli.New().Run(ctx, os.Args[1:]))
}
