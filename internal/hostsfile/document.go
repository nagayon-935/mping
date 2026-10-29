// Package hostsfile reads mping's hosts file (-f): the YAML document with
// its settings, hosts and groups, the host patterns it may use (ranges,
// CIDRs, {N..M}), and the host lists it includes (plain lists or NetBox IP
// address exports). Every file is read with the invoking user's
// permissions, since mping may run setuid root.
//
// It only decodes and expands the document; cmd/main applies the settings
// and validates the result.
package hostsfile

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Entry is one entry of a hosts-file's hosts:/groups[].hosts: list. It
// unmarshals from either a bare scalar string (the common case: just a
// hostname/IP) or a mapping with a required 'host' key and an optional
// 'dscp' key — the per-target DSCP override that lets the same destination
// be monitored under two different DSCP markings side by side (e.g. one
// entry with dscp: EF, another with dscp: CS0, both host: the same address;
// see targetSpec.DSCP).
type Entry struct {
	Host string
	// Name is an optional display name shown instead of Host (from the
	// mapping form's 'name' key, or an include file's second column).
	Name string
	// DSCP is the raw dscp: value for this entry (a name like "EF" or a
	// bare number), or "" when absent — parsed lazily, same rationale as
	// config.dscp.
	DSCP string
	// netBox retains source naming data until all includes have been loaded.
	netBox *netBoxRow
}

// UnmarshalYAML implements yaml.Unmarshaler. Field names are checked
// manually (rather than via value.Decode into a strict struct) so an
// unknown key inside a host mapping is rejected the same way
// dec.KnownFields(true) rejects one at the top level of the file.
func (h *Entry) UnmarshalYAML(value *yaml.Node) error {
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

// Group represents a named host group in the YAML config file.
type Group struct {
	Name    string      `yaml:"name"`
	Hosts   []Entry     `yaml:"hosts"`
	Include IncludeList `yaml:"include"`
}

// Document is a decoded hosts file. After Parse, Hosts and
// every group's Hosts hold concrete, expanded hosts and Include is empty.
type Document struct {
	Hosts               []Entry     `yaml:"hosts"`
	Include             IncludeList `yaml:"include"`
	Groups              []Group     `yaml:"groups"`
	IntervalMs          *int        `yaml:"interval"`
	TimeoutMs           *int        `yaml:"timeout"`
	OutputFile          *string     `yaml:"output"`
	IfaceName           *string     `yaml:"interface"`
	SourceAddr          *string     `yaml:"source"`
	PacketSize          *int        `yaml:"size"`
	Count               *int        `yaml:"count"`
	MtuEnabled          *bool       `yaml:"discovery-mtu"`
	Trace               *bool       `yaml:"traceroute"`
	AsnEnabled          *bool       `yaml:"asn"`
	PtrEnabled          *bool       `yaml:"ptr"`
	Ipv4Only            *bool       `yaml:"ipv4"`
	Ipv6Only            *bool       `yaml:"ipv6"`
	PortSpecs           []string    `yaml:"port"`
	HTTPURLs            []string    `yaml:"http"`
	JsonOutput          *string     `yaml:"json-output"`
	Mtr                 *bool       `yaml:"mtr"`
	DNSServer           *string     `yaml:"dns-server"`
	ResolveAll          *bool       `yaml:"resolve-all"`
	Duration            *string     `yaml:"duration"`
	DSCP                *string     `yaml:"dscp"`
	Web                 *bool       `yaml:"web"`
	WebPort             *int        `yaml:"web-port"`
	Thresholds          *Thresholds `yaml:"thresholds"`
	HealthFailures      *int        `yaml:"health-failures"`
	HealthRecoveries    *int        `yaml:"health-recoveries"`
	HealthWindow        *string     `yaml:"health-window"`
	EventOutput         *string     `yaml:"event-output"`
	HTTPStatus          *string     `yaml:"http-status"`
	HTTPFollowRedirects *bool       `yaml:"http-follow-redirects"`
	HTTPHeaders         []string    `yaml:"http-header"`
}

// Thresholds mirrors the stats.Thresholds boundaries in the YAML config.
// RTT/Jitter values are milliseconds; loss values are percentages.
type Thresholds struct {
	RTTWarn    *int     `yaml:"rtt-warn"`
	RTTCrit    *int     `yaml:"rtt-crit"`
	JitterWarn *int     `yaml:"jitter-warn"`
	JitterCrit *int     `yaml:"jitter-crit"`
	LossWarn   *float64 `yaml:"loss-warn"`
	LossCrit   *float64 `yaml:"loss-crit"`
}

// Parse reads, decodes, and expands a hosts file: host patterns
// (ranges, CIDRs, {N..M}) become concrete hosts and include files are read
// relative to the hosts file's directory. A CSV host list or NetBox IP
// addresses CSV export may also be passed directly via -f / --file.
func Parse(path string) (Document, error) {
	data, err := readFileAsRealUser(path)
	if err != nil {
		return Document{}, fmt.Errorf("read hosts file %q: %w", path, err)
	}

	if len(data) > 0 && (strings.EqualFold(filepath.Ext(path), ".csv") || isNetBoxCSVData(data)) {
		entries, err := parseIncludeData(path, "hosts file", data)
		if err != nil {
			return Document{}, err
		}
		doc := Document{Hosts: entries}
		resolveNetBoxDocNames(&doc)
		return doc, nil
	}

	var doc Document
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject typos/unknown keys instead of silently dropping them
	if err := dec.Decode(&doc); err != nil && err != io.EOF {
		return Document{}, fmt.Errorf("parse hosts file %q: %w", path, err)
	}
	expanded, err := expandHostsDoc(doc, filepath.Dir(path))
	if err != nil {
		return Document{}, fmt.Errorf("hosts file %q: %w", path, err)
	}
	return expanded, nil
}
