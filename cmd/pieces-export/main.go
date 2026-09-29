package main

import (
	"context"
	"os"
	"os/signal"
	_ "time/tzdata"

	"github.com/pieces-app/export-tool/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, version))
}
