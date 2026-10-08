package main

import (
	"context"
	"net/netip"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
)

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

func initTargets(specs []targetSpec) []*stats.TargetStats {
	targets := make([]*stats.TargetStats, 0, len(specs))
	for _, spec := range specs {
		targets = append(targets, stats.NewTargetStats(spec.display()))
	}
	return targets
}

func expandTargets(specs []targetSpec, groups []ui.TargetGroup, cfg config) ([]targetSpec, []ui.TargetGroup, error) {
	if !cfg.resolveAll {
		return specs, groups, nil
	}

	resolver := newCustomResolver(cfg.dnsServer, resolverBindConfig(cfg, specs))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resolvedIPs := make(map[string][]string)
	for _, spec := range specs {
		if spec.PinnedIP != "" {
			continue
		}
		rawHost := spec.Host
		if _, err := netip.ParseAddr(rawHost); err == nil {
			resolvedIPs[spec.Host] = []string{rawHost}
			continue
		}

		network := resolveNetwork(cfg)
		ips, err := resolver.LookupIP(ctx, network, rawHost)
		if err != nil || len(ips) == 0 {
			resolvedIPs[spec.Host] = []string{rawHost}
			continue
		}

		var ipStrs []string
		for _, ip := range ips {
			ipStrs = append(ipStrs, ip.String())
		}
		resolvedIPs[spec.Host] = ipStrs
	}

	var expandedSpecs []targetSpec
	expansionMap := make(map[int][]int)

	for i, spec := range specs {
		if spec.PinnedIP != "" {
			expandedSpecs = append(expandedSpecs, spec)
			expansionMap[i] = []int{len(expandedSpecs) - 1}
			continue
		}

		ips := resolvedIPs[spec.Host]
		startIdx := len(expandedSpecs)

		for _, ip := range ips {
			if spec.Host != ip {
				expandedSpecs = append(expandedSpecs, targetSpec{Host: spec.Host, PinnedIP: ip, Name: spec.Name, DSCP: spec.DSCP})
			} else {
				expandedSpecs = append(expandedSpecs, targetSpec{Host: ip, Name: spec.Name, DSCP: spec.DSCP})
			}
		}

		endIdx := len(expandedSpecs)
		var indices []int
		for j := startIdx; j < endIdx; j++ {
			indices = append(indices, j)
		}
		expansionMap[i] = indices
	}

	var expandedGroups []ui.TargetGroup
	for _, g := range groups {
		var newIndices []int
		for _, oldIdx := range g.Indices {
			newIndices = append(newIndices, expansionMap[oldIdx]...)
		}
		expandedGroups = append(expandedGroups, ui.TargetGroup{
			Name:    g.Name,
			Indices: newIndices,
		})
	}

	return expandedSpecs, expandedGroups, nil
}
