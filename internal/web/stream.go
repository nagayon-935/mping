package web

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// handleStream pushes a "snapshot" event on connect and then whenever the
// backend's snapshot tag moves, polling at cfg.streamInterval so a busy
// pinger never produces more than one event per interval.
//
// The tag includes stats.Generation(), which is process-wide and bumped by
// every probe result, so while pings are running in practice an event goes
// out on every poll; "nothing changed" only holds when all probing is idle
// (stopped, finished --count, or between reloads).
//
// Idle connections get a comment line every cfg.heartbeatInterval.
func handleStream(ctx context.Context, snapshots *snapshotFetcher, cfg handlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if err := rc.Flush(); err != nil {
			return
		}

		poll := time.NewTicker(cfg.streamInterval)
		defer poll.Stop()
		heartbeat := time.NewTicker(cfg.heartbeatInterval)
		defer heartbeat.Stop()

		var sent bool
		var last string
		send := func(fresh bool) error {
			body, key, err := snapshots.get(fresh)
			if err != nil || (sent && key == last) {
				// No provider yet, or nothing new. A failed fetch is
				// retried on the next poll rather than ending the stream.
				return nil
			}
			if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", body); err != nil {
				return err
			}
			sent, last = true, key
			return rc.Flush()
		}

		if send(false) != nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				// The server is shutting down, typically right after
				// MarkStopped and well inside one poll interval: push the
				// final state so the browser shows "stopped" rather than a
				// bare disconnect. A slow client cannot hold this up for
				// long: Close bounds Shutdown and then force-closes.
				_ = send(true)
				return
			case <-r.Context().Done():
				return
			case <-poll.C:
				if send(false) != nil {
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
