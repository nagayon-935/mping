package main

// targetSpec identifies one monitored target as it flows through cmd/main's
// host pipeline: CLI/YAML hosts → --resolve-all expansion → per-iteration
// target construction (TD-24). Host is what the user configured (hostname
// or literal IP); PinnedIP is set only for a --resolve-all expansion entry
// that pins this entry to one specific resolved address distinct from Host.
type targetSpec struct {
	Host     string
	PinnedIP string
	// Name is an optional display name from the hosts file (hostEntry.Name).
	// When set it replaces Host in display(); the pinger still reaches
	// Host, via the display→address map built by buildPingerOptions.
	Name string
	// DSCP is this target's raw per-target dscp: override (a name like
	// "EF" or a bare number), sourced from a hosts-file mapping entry
	// (hostEntry.DSCP). "" means no override — the target falls back to
	// config.dscp, mping's global default. Parsed lazily by
	// buildPingerOptions, same rationale as config.dscp.
	DSCP string
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
// stats.NewTargetStats: Name (or Host when unnamed), followed by " (ip)" for
// a pinned entry.
func (t targetSpec) display() string {
	label := t.Host
	if t.Name != "" {
		label = t.Name
	}
	if t.PinnedIP != "" {
		return label + " (" + t.PinnedIP + ")"
	}
	return label
}
