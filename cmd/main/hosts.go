package main

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/nagayon-935/mping/internal/hostsfile"
	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/spf13/pflag"
)

// targetSpec identifies one monitored target as it flows through cmd/main's
// host pipeline: CLI/YAML hosts → --resolve-all expansion → per-iteration
// target construction. Host is what the user configured (hostname
// or literal IP); PinnedIP is set only for a --resolve-all expansion entry
// that pins this entry to one specific resolved address distinct from Host.
type targetSpec struct {
	Host     string
	PinnedIP string
	// Name is an optional display name from the hosts file (hostsfile.Entry.Name).
	// When set it replaces Host in display(); the pinger still reaches
	// Host, via the display→address map built by buildPingerOptions.
	Name string
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

// mergeHosts merges a hosts-file's configuration into cfg and host list.
// It returns the full host list (ungrouped hosts first, then group hosts),
// the TargetGroup slice for grouped display, and the merged config.
func mergeHosts(cfg config, fs *pflag.FlagSet, hosts []string) ([]targetSpec, []ui.TargetGroup, config, error) {
	var doc hostsfile.Document
	if cfg.hostsFile != "" {
		var err error
		doc, err = hostsfile.Parse(cfg.hostsFile)
		if err != nil {
			return nil, nil, cfg, err
		}
	}
	return mergeHostsDocument(cfg, fs, doc, hosts)
}

func mergeHostsDocument(cfg config, fs *pflag.FlagSet, doc hostsfile.Document, cliHosts []string) ([]targetSpec, []ui.TargetGroup, config, error) {
	cfg, err := applyDocToCfg(cfg, fs, doc)
	if err != nil {
		return nil, nil, cfg, err
	}
	if err := validateMergedHosts(cfg, doc.Hosts, doc.Groups, cliHosts); err != nil {
		return nil, nil, cfg, err
	}
	specs, groups := buildHostsAndGroups(doc.Hosts, doc.Groups, cliHosts)
	return specs, groups, cfg, nil
}

// buildHostsAndGroups assembles the final host list and TargetGroup slice.
// Ungrouped hosts (docHosts + cliHosts) come first; group hosts are appended
// after, with indices pointing into the combined slice.
func buildHostsAndGroups(docHosts []hostsfile.Entry, docGroups []hostsfile.Group, cliHosts []string) ([]targetSpec, []ui.TargetGroup) {
	allHosts := make([]targetSpec, 0, len(docHosts)+len(cliHosts))
	for _, h := range docHosts {
		allHosts = append(allHosts, targetSpec{Host: h.Host, Name: h.Name})
	}
	for _, h := range cliHosts {
		allHosts = append(allHosts, targetSpec{Host: h})
	}
	var uiGroups []ui.TargetGroup
	for _, g := range docGroups {
		startIdx := len(allHosts)
		for _, h := range g.Hosts {
			allHosts = append(allHosts, targetSpec{Host: h.Host, Name: h.Name})
		}
		indices := make([]int, len(g.Hosts))
		for j := range g.Hosts {
			indices[j] = startIdx + j
		}
		uiGroups = append(uiGroups, ui.TargetGroup{Name: g.Name, Indices: indices})
	}
	return allHosts, uiGroups
}

// buildTargetsForIteration initializes the statistics before startup PMTU probes run.
func buildTargetsForIteration(specs []targetSpec, cfg config) []*stats.TargetStats {
	targets := make([]*stats.TargetStats, len(specs))
	for i, spec := range specs {
		targets[i] = newTarget(spec, cfg)
	}
	return targets
}

func newTarget(spec targetSpec, cfg config) *stats.TargetStats {
	t := stats.NewTargetStats(spec.display())
	t.SetResolveAddress(spec.resolveAddr())
	t.ConfigureHealth(healthPolicy(cfg))
	dnsServer := cfg.dnsServer
	// Match pinger.ResolveIPAddrContext: zoned IPv6 literals skip DNS too.
	if _, err := netip.ParseAddr(spec.Host); err == nil {
		dnsServer = "-"
	} else if dnsServer == "" {
		dnsServer = "Default"
	}
	t.SetDNSServer(dnsServer)
	return t
}

// expandTargets keeps unresolved names available for the pinger to retry.
func expandTargets(specs []targetSpec, groups []ui.TargetGroup, cfg config) ([]targetSpec, []ui.TargetGroup) {
	if !cfg.resolveAll {
		return specs, groups
	}
	resolver := newCustomResolver(cfg.dnsServer, resolverBindConfig(cfg, specs))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	network := resolveNetwork(cfg)
	return expandHostAddresses(specs, groups, func(host string) []net.IP {
		ips, err := resolver.LookupIP(ctx, network, host)
		if err != nil {
			return nil
		}
		return ips
	})
}

func expandHostAddresses(specs []targetSpec, groups []ui.TargetGroup, lookup func(string) []net.IP) ([]targetSpec, []ui.TargetGroup) {
	resolved := make(map[string][]net.IP)
	var expanded []targetSpec
	// All addresses for specs[i] occupy [offsets[i], offsets[i+1]).
	offsets := make([]int, len(specs)+1)
	for i, spec := range specs {
		offsets[i] = len(expanded)
		if _, err := netip.ParseAddr(spec.Host); spec.PinnedIP != "" || err == nil {
			expanded = append(expanded, spec)
			continue
		}
		ips, found := resolved[spec.Host]
		if !found {
			ips = lookup(spec.Host)
			resolved[spec.Host] = ips
		}
		if len(ips) == 0 {
			expanded = append(expanded, spec)
			continue
		}
		for _, ip := range ips {
			spec.PinnedIP = ip.String()
			expanded = append(expanded, spec)
		}
	}
	offsets[len(specs)] = len(expanded)

	var expandedGroups []ui.TargetGroup
	for _, g := range groups {
		group := ui.TargetGroup{Name: g.Name}
		for _, idx := range g.Indices {
			if idx < 0 || idx >= len(specs) {
				continue
			}
			for j := offsets[idx]; j < offsets[idx+1]; j++ {
				group.Indices = append(group.Indices, j)
			}
		}
		expandedGroups = append(expandedGroups, group)
	}
	return expanded, expandedGroups
}
