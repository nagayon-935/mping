package main

import (
	"github.com/nagayon-935/mping/internal/hostsfile"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/spf13/pflag"
)

// mergeHosts merges a hosts-file's configuration into cfg and host list.
// It returns the full host list (ungrouped hosts first, then group hosts),
// the TargetGroup slice for grouped display, and the merged config.
func mergeHosts(cfg config, fs *pflag.FlagSet, hosts []string) ([]targetSpec, []ui.TargetGroup, config, error) {
	if cfg.hostsFile == "" {
		if err := validateMergedHosts(cfg, nil, nil, hosts); err != nil {
			return nil, nil, cfg, err
		}
		specs, groups := buildHostsAndGroups(nil, nil, hosts)
		return specs, groups, cfg, nil
	}
	doc, err := hostsfile.Parse(cfg.hostsFile)
	if err != nil {
		return nil, nil, cfg, err
	}
	docHosts, docGroups, merged, err := applyDocToCfg(cfg, fs, doc)
	if err != nil {
		return nil, nil, merged, err
	}
	if err := validateMergedHosts(merged, docHosts, docGroups, hosts); err != nil {
		return nil, nil, merged, err
	}
	allHosts, uiGroups := buildHostsAndGroups(docHosts, docGroups, hosts)
	return allHosts, uiGroups, merged, nil
}

// buildHostsAndGroups assembles the final host list and TargetGroup slice.
// Ungrouped hosts (docHosts + cliHosts) come first; group hosts are appended
// after, with indices pointing into the combined slice. docHosts entries
// carry their per-target DSCP override (if any) straight into the returned
// targetSpec; cliHosts (bare strings from argv) never have one.
func buildHostsAndGroups(docHosts []hostsfile.Entry, docGroups []hostsfile.Group, cliHosts []string) ([]targetSpec, []ui.TargetGroup) {
	allHosts := make([]targetSpec, 0, len(docHosts)+len(cliHosts))
	for _, h := range docHosts {
		allHosts = append(allHosts, targetSpec{Host: h.Host, Name: h.Name, DSCP: h.DSCP})
	}
	for _, h := range cliHosts {
		allHosts = append(allHosts, targetSpec{Host: h})
	}
	var uiGroups []ui.TargetGroup
	for _, g := range docGroups {
		startIdx := len(allHosts)
		for _, h := range g.Hosts {
			allHosts = append(allHosts, targetSpec{Host: h.Host, Name: h.Name, DSCP: h.DSCP})
		}
		indices := make([]int, len(g.Hosts))
		for j := range g.Hosts {
			indices[j] = startIdx + j
		}
		uiGroups = append(uiGroups, ui.TargetGroup{Name: g.Name, Indices: indices})
	}
	return allHosts, uiGroups
}
