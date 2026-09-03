package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/fundus-app/fundus/internal/model"
)

// handleEvents streams core events as Server-Sent Events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "internal", "streaming unsupported")
		return
	}
	events, cancel := s.core.Subscribe()
	if events == nil {
		writeError(w, http.StatusServiceUnavailable, "too_many_subscribers", "too many event streams open")
		return
	}
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	fmt.Fprintf(w, "event: hello\ndata: %s\n\n", mustJSON(map[string]any{"seq": s.core.Seq(), "version": Version}))
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.baseCtx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if rec, ok := ev.Payload.(*model.Receipt); ok && ev.Type == "txn.committed" {
				fmt.Fprintf(w, "id: %d\n", rec.Seq)
			}
			if cap, ok := ev.Payload.(*model.Capture); ok && ev.Type == "capture.changed" {
				// Clients render the inbox pill from this event alone.
				ev.Payload = s.captureWithReceipts(cap)
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, mustJSON(ev))
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}
