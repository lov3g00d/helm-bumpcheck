package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/lov3g00d/helm-bumpcheck/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := cli.NewRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		os.Exit(1)
	}
}
