package main

import (
	"bufio"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/nagayon-935/mping/internal/pinger"
	"gopkg.in/yaml.v3"
)

// includeList is a hosts-file include: value — one path or a list of paths.
type includeList []string

// UnmarshalYAML accepts either a scalar or a sequence of scalars.
func (l *includeList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Value == "" {
			return fmt.Errorf("include: line %d: empty file path", value.Line)
		}
		*l = includeList{value.Value}
		return nil
	case yaml.SequenceNode:
		paths := make(includeList, 0, len(value.Content))
		for _, item := range value.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("include: line %d: expected a file path", item.Line)
			}
			if item.Value == "" {
				return fmt.Errorf("include: line %d: empty file path", item.Line)
			}
			paths = append(paths, item.Value)
		}
		*l = paths
		return nil
	default:
		return fmt.Errorf("include: line %d: expected a file path or a list of file paths", value.Line)
	}
}

// resolveIncludePath resolves an include path relative to the directory of
// the hosts file that names it.
func resolveIncludePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(baseDir, path)
}

// readIncludeFile reads a host list file and returns its expanded entries.
// Each non-blank line is "host[,name[,dscp]]"; '#' starts a comment; a first
// line whose host column is "host" or "hostname" (any case) is a header and
// skipped. Hosts may use the same range/CIDR/brace patterns as the YAML
// file, and every expanded host must be an IP address or a hostname.
//
// mping may run setuid root (macOS install.sh), so the file is read as the
// invoking user (asRealUser), errors report the file and line number but
// never the line's text, and the hostname check keeps arbitrary text lines
// from being displayed as targets.
func readIncludeFile(path string) ([]hostEntry, error) {
	var out []hostEntry
	err := asRealUser(func() error {
		var err error
		out, err = readIncludeFileUnprivileged(path)
		return err
	})
	return out, err
}

func readIncludeFileUnprivileged(path string) ([]hostEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("include %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("include %q: not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("include %q: %w", path, err)
	}
	defer f.Close()

	out := []hostEntry{}
	sc := bufio.NewScanner(f)
	for lineNo, sawData := 1, false; sc.Scan(); lineNo++ {
		entry, ok, err := parseIncludeLine(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("include %q line %d: %w", path, lineNo, err)
		}
		if !ok {
			continue
		}
		if !sawData {
			sawData = true
			if strings.EqualFold(entry.Host, "host") || strings.EqualFold(entry.Host, "hostname") {
				continue
			}
		}
		if entry.DSCP != "" {
			if _, err := pinger.ParseDSCP(entry.DSCP); err != nil {
				return nil, fmt.Errorf("include %q line %d: invalid dscp (expected a DSCP name such as EF or a number 0-63)", path, lineNo)
			}
		}
		expanded, err := expandHostEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("include %q line %d: %w", path, lineNo, err)
		}
		for _, e := range expanded {
			if !isHostSyntax(e.Host) {
				return nil, fmt.Errorf("include %q line %d: host is not a valid IP address or hostname", path, lineNo)
			}
		}
		if len(out)+len(expanded) > maxHostsFileTargets {
			return nil, fmt.Errorf("include %q: expands to more than %d targets", path, maxHostsFileTargets)
		}
		out = append(out, expanded...)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("include %q: %w", path, err)
	}
	return out, nil
}

// parseIncludeLine splits one include-file line into its columns (DSCP is
// validated by the caller, after header detection). ok is false for blank
// and comment-only lines. Errors never quote the line.
func parseIncludeLine(line string) (entry hostEntry, ok bool, err error) {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return hostEntry{}, false, nil
	}
	cols := strings.Split(line, ",")
	if len(cols) > 3 {
		return hostEntry{}, false, fmt.Errorf("expected at most 3 columns (host,name,dscp), got %d", len(cols))
	}
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}
	entry.Host = cols[0]
	if entry.Host == "" {
		return hostEntry{}, false, errors.New("host column is empty")
	}
	if len(cols) > 1 {
		entry.Name = cols[1]
	}
	if len(cols) > 2 {
		entry.DSCP = cols[2]
	}
	return entry, true, nil
}

// isHostSyntax reports whether s is an IP address (optionally with an IPv6
// zone) or a plausible hostname: 1-253 characters of letters, digits, '.',
// '-' and '_' (the last for SRV-style labels).
func isHostSyntax(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// expandHostsDoc expands every host pattern in doc and appends each list's
// include files (resolved against baseDir) after its inline entries. The
// returned doc lists concrete hosts only and has no includes left.
func expandHostsDoc(doc hostsFileYAML, baseDir string) (hostsFileYAML, error) {
	hosts, err := collectHosts(doc.Hosts, doc.Include, "hosts", baseDir)
	if err != nil {
		return hostsFileYAML{}, err
	}
	total := len(hosts)
	groups := make([]groupYAML, 0, len(doc.Groups))
	for _, g := range doc.Groups {
		groupHosts, err := collectHosts(g.Hosts, g.Include, fmt.Sprintf("groups[%q]", g.Name), baseDir)
		if err != nil {
			return hostsFileYAML{}, err
		}
		total += len(groupHosts)
		groups = append(groups, groupYAML{Name: g.Name, Hosts: groupHosts})
	}
	if total > maxHostsFileTargets {
		return hostsFileYAML{}, fmt.Errorf("hosts file expands to %d targets, more than %d targets allowed", total, maxHostsFileTargets)
	}
	doc.Hosts, doc.Groups, doc.Include = hosts, groups, nil
	return doc, nil
}

func collectHosts(entries []hostEntry, includes includeList, location, baseDir string) ([]hostEntry, error) {
	hosts, err := expandHostEntries(entries, location)
	if err != nil {
		return nil, err
	}
	for _, inc := range includes {
		included, err := readIncludeFile(resolveIncludePath(baseDir, inc))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", location, err)
		}
		hosts = append(hosts, included...)
	}
	return hosts, nil
}

// hostsFileWatchPaths returns the hosts file plus every include file it
// names (deduplicated, in order of appearance), for the reload watcher.
// Include files that don't exist yet are still listed so that creating one
// triggers a reload. If the hosts file can't be read or parsed, only the
// hosts file itself is returned — fixing it triggers the reload that
// re-derives the include set.
func hostsFileWatchPaths(path string) []string {
	paths := []string{path}
	var data []byte
	if err := asRealUser(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	}); err != nil {
		return paths
	}
	var doc struct {
		Include includeList `yaml:"include"`
		Groups  []struct {
			Include includeList `yaml:"include"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return paths
	}
	all := append(includeList(nil), doc.Include...)
	for _, g := range doc.Groups {
		all = append(all, g.Include...)
	}
	baseDir := filepath.Dir(path)
	seen := map[string]bool{path: true}
	for _, inc := range all {
		p := resolveIncludePath(baseDir, inc)
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}
