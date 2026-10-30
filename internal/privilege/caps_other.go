//go:build unix && !linux

package privilege

// capabilities is empty where the kernel has no Linux-style capabilities.
func capabilities() (string, error) { return "", nil }
