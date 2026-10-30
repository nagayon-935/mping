package main

import "time"

// clock returns the current wall-clock time in the HH:MM:SS form used by every
// TUI Log line.
func clock() string {
	return time.Now().Format("15:04:05")
}

// postLog sends line to ch without ever blocking: when the channel is full (or
// nil) the line is dropped. Log notices are best-effort, and a stalled TUI must
// never wedge the goroutine posting them.
func postLog(ch chan<- string, line string) {
	select {
	case ch <- line:
	default:
	}
}
