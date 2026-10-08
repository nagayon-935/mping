//go:build !unix

package main

// realUserCanRead is a no-op where setuid doesn't exist (e.g. Windows): the
// process always reads files as the invoking user.
var realUserCanRead = func(string) error { return nil }
