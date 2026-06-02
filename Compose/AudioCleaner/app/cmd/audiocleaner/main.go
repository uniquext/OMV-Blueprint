package main

import (
	"fmt"
	"log"
	"os"

	"github.com/fsnotify/fsnotify"
	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
	"omv-blueprint/compose/audiocleaner/internal/config"
)

var (
	// Keep planned service dependencies in the module graph for this skeleton.
	_ = fsnotify.NewWatcher
	_ = chi.NewRouter
)

func main() {
	cfg, err := config.LoadOrCreate(getenv("CONFIG_PATH", "/app/config/config.json"))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("AudioCleaner loaded %d media roots\n", len(cfg.Media.Roots))
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
