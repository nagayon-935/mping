package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/nagayon-935/mping/internal/stats"
)

// Probe callbacks never wait for disk. Overflow is reported explicitly instead
// of hiding lost records or holding a target lock during a slow write.
func startEventOutput(path string, errOut io.Writer) (func(uint64, string, stats.Event), func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	var file *os.File
	err := asRealUser(func() error {
		var err error
		file, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		return err
	})
	if err != nil {
		return nil, nil, fmt.Errorf("event-output: %w", err)
	}
	type record struct {
		stats.Event
		Host string `json:"host"`
	}
	queue := make(chan record, 1024)
	done := make(chan struct{})
	var dropped atomic.Uint64
	go func() {
		defer close(done)
		encoder := json.NewEncoder(file)
		failed := false
		for e := range queue {
			if failed {
				continue
			}
			if err := encoder.Encode(e); err != nil {
				fmt.Fprintf(errOut, "event-output: %v\n", err)
				failed = true
			}
		}
		if err := file.Close(); err != nil {
			fmt.Fprintf(errOut, "event-output: %v\n", err)
		}
	}()
	sink := func(_ uint64, host string, e stats.Event) {
		select {
		case queue <- record{e, host}:
		default:
			dropped.Add(1)
		}
	}
	// Called after all measurement writers have been joined.
	closeOutput := func() {
		close(queue)
		<-done
		if n := dropped.Load(); n > 0 {
			fmt.Fprintf(errOut, "event-output: dropped %d events because the writer was busy\n", n)
		}
	}
	return sink, closeOutput, nil
}
