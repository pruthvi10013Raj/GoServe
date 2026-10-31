package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"goserve/internal/app"
)

func main() {
	if err := runWithSignals(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runWithSignals(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return app.Run(ctx, args)
}
