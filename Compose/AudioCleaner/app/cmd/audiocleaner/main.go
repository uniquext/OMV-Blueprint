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

var (
	notifyContext = signal.NotifyContext
	runApp        = app.Run
	fatal         = log.Fatal
)

func main() {
	ctx, stop := notifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runApp(ctx, app.Options{}); err != nil && !errors.Is(err, context.Canceled) {
		fatal(err)
	}
}
