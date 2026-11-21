// Package hostsfile reads mping's hosts file (-f): the YAML document with
// its settings, hosts and groups, the host patterns it may use (ranges,
// CIDRs, {N..M}), and any NetBox IP address CSV exports it includes.
// Every file is read with the invoking user's
// permissions, since mping may run setuid root.
//
// It only decodes and expands the document; cmd/main applies the settings
// and validates the result.
package hostsfile

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nagayon-935/mping/internal/privilege"
	"gopkg.in/yaml.v3"
)

// Entry is one entry of a hosts-file's hosts:/groups[].hosts: list. It
// unmarshals from either a bare scalar string (the common case: just a
// hostname/IP) or a mapping with a required 'host' key and an optional
// 'name' key.
type Entry struct {
	Host string
	// Name is an optional display name shown instead of Host (from the
	// mapping form's 'name' key or derived from a NetBox CSV row).
	Name string
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
		return fmt.Errorf("host entry: expected a string or a mapping with 'host'/'name' keys, got %v", value.Kind)
	}
	seen := make(map[string]bool)
	for i := 0; i+1 < len(value.Content); i += 2 {
		key, val := value.Content[i].Value, value.Content[i+1]
		if seen[key] {
			return fmt.Errorf("line %d: duplicate key %q in host entry", value.Content[i].Line, key)
		}
		seen[key] = true
		switch key {
		case "host":
			h.Host = val.Value
		case "name":
			h.Name = val.Value
		default:
			return fmt.Errorf("host entry: unknown field %q (expected 'host' or 'name')", key)
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
	Mtr                 *bool       `yaml:"mtr"`
	DNSServer           *string     `yaml:"dns-server"`
	ResolveAll          *bool       `yaml:"resolve-all"`
	Duration            *string     `yaml:"duration"`
	Web                 *bool       `yaml:"web"`
	WebPort             *int        `yaml:"web-port"`
	Thresholds          *Thresholds `yaml:"thresholds"`
	HealthFailures      *int        `yaml:"health-failures"`
	HealthRecoveries    *int        `yaml:"health-recoveries"`
	HealthWindow        *string     `yaml:"health-window"`
	HTTPFollowRedirects *bool       `yaml:"http-follow-redirects"`
}

// removedSettings are keys earlier versions accepted, with what to tell a
// user whose file still has them. They are rejected by name so the error
// says the setting was removed, not that it was mistyped.
var removedSettings = map[string]string{
	"dscp":         "DSCP marking is no longer supported; delete the key",
	"http-status":  "HTTP checks always treat 200-399 as up; delete the key",
	"http-header":  "custom HTTP request headers are no longer supported; delete the key",
	"output":       "save a statistics snapshot from the TUI or the Web UI instead; delete the key",
	"json-output":  "save a statistics snapshot from the TUI or the Web UI instead; delete the key",
	"event-output": "save a statistics snapshot from the TUI or the Web UI instead; delete the key",
}

// checkRemovedSettings rejects a document that still uses a removed
// setting, at the top level or in a host entry, naming its line. Syntax
// errors are left to the strict decode.
func checkRemovedSettings(data []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil
	}
	return findRemovedKey(&root)
}

func findRemovedKey(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if key := n.Content[i]; removedSettings[key.Value] != "" {
				return fmt.Errorf("line %d: %q was removed: %s", key.Line, key.Value, removedSettings[key.Value])
			}
		}
	}
	for _, c := range n.Content {
		if err := findRemovedKey(c); err != nil {
			return err
		}
	}
	return nil
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
// relative to the hosts file's directory. A NetBox IP addresses CSV export
// may also be passed directly via -f / --file.
func Parse(path string) (Document, error) {
	data, err := readFileAsRealUser(path)
	if err != nil {
		return Document{}, fmt.Errorf("read hosts file %q: %w", path, err)
	}

	if len(data) > 0 && (strings.EqualFold(filepath.Ext(path), ".csv") || isNetBoxCSVData(data)) {
		entries, err := parseNetBoxCSV(path, "hosts file", data)
		if err != nil {
			return Document{}, err
		}
		doc := Document{Hosts: entries}
		resolveNetBoxDocNames(&doc)
		return doc, nil
	}

	if err := checkRemovedSettings(data); err != nil {
		return Document{}, fmt.Errorf("parse hosts file %q: %w", path, err)
	}
	var doc Document
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject typos/unknown keys instead of silently dropping them
	if err := dec.Decode(&doc); err != nil && err != io.EOF {
		return Document{}, fmt.Errorf("parse hosts file %q: %w", path, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		const reason = "hosts file must contain a single YAML document"
		if err != nil {
			return Document{}, fmt.Errorf("parse hosts file %q: %s: %w", path, reason, err)
		}
		return Document{}, fmt.Errorf("parse hosts file %q: line %d: %s", path, extra.Line, reason)
	}
	expanded, err := expandHostsDoc(doc, filepath.Dir(path))
	if err != nil {
		return Document{}, fmt.Errorf("hosts file %q: %w", path, err)
	}
	return expanded, nil
}

// asRealUser runs every hosts-file and include-file access as the invoking
// user. A setuid-root mping would otherwise read any path a hosts file
// names with root's permissions; with it, the kernel applies the invoking
// user's permissions instead. Tests swap it to verify that every file
// access goes through it.
var asRealUser = privilege.AsRealUser

// readFileAsRealUser reads path as the invoking user. Errors are returned
// unwrapped so each caller keeps its own message.
func readFileAsRealUser(path string) ([]byte, error) {
	var data []byte
	err := asRealUser(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}
