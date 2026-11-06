//go:build !darwin && !linux

package pinger

import "syscall"

// Tests may override this to exercise the source-address fallback.
var interfaceBindingSupported = false

// Native interface binding is unavailable; sockets instead bind the matching
// SourceV4/SourceV6 address when no explicit Source was given.
func bindRawConnToInterface(_ syscall.RawConn, _ string, _ bool) {}
