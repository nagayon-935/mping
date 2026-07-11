package main

// targetSpec identifies one monitored target as it flows through cmd/main's
// host pipeline: CLI/YAML hosts → --resolve-all expansion → per-iteration
// target construction (TD-24). Host is what the user configured (hostname
// or literal IP); PinnedIP is set only for a --resolve-all expansion entry
// that pins this entry to one specific resolved address distinct from Host.
type targetSpec struct {
	Host     string
	PinnedIP string
}

// resolveAddr returns the address DNS/dial resolution should use: the
// pinned IP when set, otherwise Host itself.
func (t targetSpec) resolveAddr() string {
	if t.PinnedIP != "" {
		return t.PinnedIP
	}
	return t.Host
}

// display returns the string shown to the user and handed to
// stats.NewTargetStats: "host (ip)" for a pinned entry, otherwise just Host.
func (t targetSpec) display() string {
	if t.PinnedIP != "" {
		return t.Host + " (" + t.PinnedIP + ")"
	}
	return t.Host
}
