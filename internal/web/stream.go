package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// handleStream pushes a "snapshot" event on connect and again whenever the
// stats generation or the source state changes, polling at cfg.streamInterval
// so a busy pinger never produces more than one event per interval. Idle
// connections get a comment line every cfg.heartbeatInterval.
func handleStream(ctx context.Context, src *Source, cfg handlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		if err := rc.Flush(); err != nil {
			return
		}

		poll := time.NewTicker(cfg.streamInterval)
		defer poll.Stop()
		heartbeat := time.NewTicker(cfg.heartbeatInterval)
		defer heartbeat.Stop()

		var sent bool
		var lastGen, lastVersion uint64
		send := func() error {
			p, _, version := src.load()
			gen := stats.Generation()
			if p == nil || (sent && gen == lastGen && version == lastVersion) {
				return nil
			}
			snap, err := buildSnapshot(src)
			if err != nil {
				return nil
			}
			data, err := json.Marshal(snap)
			if err != nil {
				return fmt.Errorf("encode snapshot: %w", err)
			}
			if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data); err != nil {
				return err
			}
			sent, lastGen, lastVersion = true, gen, version
			return rc.Flush()
		}

		if send() != nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.Context().Done():
				return
			case <-poll.C:
				if send() != nil {
					return
				}
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				if rc.Flush() != nil {
					return
				}
			}
		}
	}
}
