package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"omv-blueprint/compose/audiocleaner/internal/eventbus"
)

const sseHeartbeatInterval = 30 * time.Second

func StreamEvents(w http.ResponseWriter, r *http.Request, bus *eventbus.Bus) {
	if bus == nil {
		writeError(w, http.StatusInternalServerError, "event bus unavailable")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	events, cancel := bus.Subscribe()
	defer cancel()

	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case event := <-events:
			if err := writeSSE(w, "message", event); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, "event: heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeSSE(w http.ResponseWriter, eventType string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, encoded)
	return err
}
