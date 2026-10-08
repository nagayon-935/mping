package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// hostEntry is one entry of a hosts-file's hosts:/groups[].hosts: list. It
// unmarshals from either a bare scalar string (the pre-existing, common
// case: just a hostname/IP) or a mapping with a required 'host' key and an
// optional 'dscp' key — the per-target DSCP override that lets the same
// destination be monitored under two different DSCP markings side by side
// (e.g. one entry with dscp: EF, another with dscp: CS0, both host: the
// same address), which is this feature's primary use case (see
// targetSpec.DSCP).
type hostEntry struct {
	Host string
	// Name is an optional display name shown instead of Host (from the
	// mapping form's 'name' key, or an include file's second column).
	Name string
	// DSCP is the raw dscp: value for this entry (a name like "EF" or a
	// bare number), or "" when absent — parsed lazily, same rationale as
	// config.dscp.
	DSCP string
}

// UnmarshalYAML implements yaml.Unmarshaler. Field names are checked
// manually (rather than via value.Decode into a strict struct) so an
// unknown key inside a host mapping is rejected the same way
// dec.KnownFields(true) rejects one at the top level of the file.
func (h *hostEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		h.Host = value.Value
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("host entry: expected a string or a mapping with 'host'/'name'/'dscp' keys, got %v", value.Kind)
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		key, val := value.Content[i].Value, value.Content[i+1]
		switch key {
		case "host":
			h.Host = val.Value
		case "name":
			h.Name = val.Value
		case "dscp":
			h.DSCP = val.Value
		default:
			return fmt.Errorf("host entry: unknown field %q (expected 'host', 'name' or 'dscp')", key)
		}
	}
	if h.Host == "" {
		return fmt.Errorf("host entry: 'host' field is required when given as a mapping")
	}
	return nil
}

// groupYAML represents a named host group in the YAML config file.
type groupYAML struct {
	Name    string      `yaml:"name"`
	Hosts   []hostEntry `yaml:"hosts"`
	Include includeList `yaml:"include"`
}

// hostsFileYAML is a decoded hosts file. After parseHostsFile, Hosts and
// every group's Hosts hold concrete, expanded hosts and Include is empty.
type hostsFileYAML struct {
	Hosts      []hostEntry     `yaml:"hosts"`
	Include    includeList     `yaml:"include"`
	Groups     []groupYAML     `yaml:"groups"`
	IntervalMs *int            `yaml:"interval"`
	TimeoutMs  *int            `yaml:"timeout"`
	OutputFile *string         `yaml:"output"`
	IfaceName  *string         `yaml:"interface"`
	SourceAddr *string         `yaml:"source"`
	PacketSize *int            `yaml:"size"`
	Count      *int            `yaml:"count"`
	MtuEnabled *bool           `yaml:"discovery-mtu"`
	Trace      *bool           `yaml:"traceroute"`
	AsnEnabled *bool           `yaml:"asn"`
	PtrEnabled *bool           `yaml:"ptr"`
	Ipv4Only   *bool           `yaml:"ipv4"`
	Ipv6Only   *bool           `yaml:"ipv6"`
	PortSpecs  []string        `yaml:"port"`
	HTTPURLs   []string        `yaml:"http"`
	JsonOutput *string         `yaml:"json-output"`
	Mtr        *bool           `yaml:"mtr"`
	DNSServer  *string         `yaml:"dns-server"`
	ResolveAll *bool           `yaml:"resolve-all"`
	Duration   *string         `yaml:"duration"`
	DSCP       *string         `yaml:"dscp"`
	Thresholds *thresholdsYAML `yaml:"thresholds"`
}

// thresholdsYAML mirrors the ui.Thresholds boundaries in the YAML config.
// RTT/Jitter values are milliseconds; loss values are percentages.
type thresholdsYAML struct {
	RTTWarn    *int     `yaml:"rtt-warn"`
	RTTCrit    *int     `yaml:"rtt-crit"`
	JitterWarn *int     `yaml:"jitter-warn"`
	JitterCrit *int     `yaml:"jitter-crit"`
	LossWarn   *float64 `yaml:"loss-warn"`
	LossCrit   *float64 `yaml:"loss-crit"`
}

// parseHostsFile reads, decodes, and expands a hosts file: host patterns
// (ranges, CIDRs, {N..M}) become concrete hosts and include files are read
// relative to the hosts file's directory.
func parseHostsFile(path string) (hostsFileYAML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hostsFileYAML{}, fmt.Errorf("read hosts file %q: %w", path, err)
	}

	var doc hostsFileYAML
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject typos/unknown keys instead of silently dropping them
	if err := dec.Decode(&doc); err != nil && err != io.EOF {
		return hostsFileYAML{}, fmt.Errorf("parse hosts file %q: %w", path, err)
	}
	expanded, err := expandHostsDoc(doc, filepath.Dir(path))
	if err != nil {
		return hostsFileYAML{}, fmt.Errorf("hosts file %q: %w", path, err)
	}
	return expanded, nil
}

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
	doc, err := parseHostsFile(cfg.hostsFile)
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
func buildHostsAndGroups(docHosts []hostEntry, docGroups []groupYAML, cliHosts []string) ([]targetSpec, []ui.TargetGroup) {
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
