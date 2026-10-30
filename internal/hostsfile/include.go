package hostsfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// IncludeList is a hosts-file include: value — one path or a list of paths.
type IncludeList []string

// UnmarshalYAML accepts either a scalar or a sequence of scalars.
func (l *IncludeList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Value == "" {
			return fmt.Errorf("include: line %d: empty file path", value.Line)
		}
		*l = IncludeList{value.Value}
		return nil
	case yaml.SequenceNode:
		paths := make(IncludeList, 0, len(value.Content))
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
// the hosts file that names it, expanding a leading "~" or "~/" to the user's
// home directory when available.
func resolveIncludePath(baseDir, path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(baseDir, path)
}

// readIncludeFile reads a NetBox IP addresses CSV export named by include:
// and returns its entries; plain host lists are rejected.
//
// mping may run setuid root (macOS install.sh), so the file is read as the
// invoking user (asRealUser), and errors report the file and line number but
// never a line's text.
func readIncludeFile(path string) ([]Entry, error) {
	var out []Entry
	err := asRealUser(func() error {
		var err error
		out, err = readIncludeFileUnprivileged(path)
		return err
	})
	return out, err
}

func readIncludeFileUnprivileged(path string) ([]Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("include %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("include %q: not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("include %q: %w", path, err)
	}
	return parseNetBoxCSV(path, "include", data)
}

// expandHostsDoc expands every host pattern in doc and appends each list's
// include files (resolved against baseDir) after its inline entries. The
// returned doc lists concrete hosts only and has no includes left.
func expandHostsDoc(doc Document, baseDir string) (Document, error) {
	hosts, err := collectHosts(doc.Hosts, doc.Include, "hosts", baseDir)
	if err != nil {
		return Document{}, err
	}
	total := len(hosts)
	groups := make([]Group, 0, len(doc.Groups))
	for _, g := range doc.Groups {
		groupHosts, err := collectHosts(g.Hosts, g.Include, fmt.Sprintf("groups[%q]", g.Name), baseDir)
		if err != nil {
			return Document{}, err
		}
		total += len(groupHosts)
		groups = append(groups, Group{Name: g.Name, Hosts: groupHosts})
	}
	if total > maxHostsFileTargets {
		return Document{}, fmt.Errorf("hosts file expands to %d targets, more than %d targets allowed", total, maxHostsFileTargets)
	}
	doc.Hosts, doc.Groups, doc.Include = hosts, groups, nil
	resolveNetBoxDocNames(&doc)
	return doc, nil
}

func collectHosts(entries []Entry, includes IncludeList, location, baseDir string) ([]Entry, error) {
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

// WatchPaths returns the hosts file plus every include file it
// names (deduplicated, in order of appearance), for the reload watcher.
// Include files that don't exist yet are still listed so that creating one
// triggers a reload. If the hosts file can't be read or parsed, only the
// hosts file itself is returned — fixing it triggers the reload that
// re-derives the include set.
func WatchPaths(path string) []string {
	paths := []string{path}
	data, err := readFileAsRealUser(path)
	if err != nil {
		return paths
	}
	var doc struct {
		Include IncludeList `yaml:"include"`
		Groups  []struct {
			Include IncludeList `yaml:"include"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return paths
	}
	all := append(IncludeList(nil), doc.Include...)
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
