package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"omv-blueprint/compose/audiocleaner/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, app.Options{}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
