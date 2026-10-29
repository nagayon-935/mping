package hostsfile

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
)

type netBoxColumns struct {
	ipIdx      int
	statusIdx  int
	dnsIdx     int
	ifaceIdx   int
	parentIdxs []int
}

type netBoxRow struct {
	host     string
	baseName string
	iface    string
}

// isNetBoxCSVData reports whether data begins with a NetBox IP-addresses CSV
// header row (after an optional UTF-8 BOM and leading blank/comment lines).
func isNetBoxCSVData(data []byte) bool {
	trimmed, _ := trimLeadingCommentsAndBlanks(data)
	if len(trimmed) == 0 {
		return false
	}
	firstLine, _, _ := bytes.Cut(trimmed, []byte{'\n'})
	r := csv.NewReader(bytes.NewReader(firstLine))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return false
	}
	_, ok := parseNetBoxHeader(header)
	return ok
}

// trimLeadingCommentsAndBlanks skips an optional UTF-8 BOM, blank lines, and
// '#'-prefixed comment lines before the CSV header, returning the remaining
// slice and the number of lines skipped so line numbers stay accurate.
func trimLeadingCommentsAndBlanks(data []byte) ([]byte, int) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	skippedLines := 0
	for len(data) > 0 {
		line, rest, found := bytes.Cut(data, []byte{'\n'})
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			if !found {
				return nil, skippedLines
			}
			data = rest
			skippedLines++
			continue
		}
		break
	}
	return data, skippedLines
}

func parseNetBoxHeader(header []string) (netBoxColumns, bool) {
	cols := netBoxColumns{
		ipIdx:     -1,
		statusIdx: -1,
		dnsIdx:    -1,
		ifaceIdx:  -1,
	}
	var fallbackParents []int
	hasMarker := false
	for i, raw := range header {
		name := strings.ToLower(strings.TrimSpace(raw))
		switch name {
		case "ip address":
			if cols.ipIdx < 0 {
				cols.ipIdx = i
			}
			hasMarker = true
		case "address":
			if cols.ipIdx < 0 {
				cols.ipIdx = i
			}
		case "status":
			if cols.statusIdx < 0 {
				cols.statusIdx = i
			}
			hasMarker = true
		case "dns name", "dns_name":
			if cols.dnsIdx < 0 {
				cols.dnsIdx = i
			}
			hasMarker = true
		case "interface":
			if cols.ifaceIdx < 0 {
				cols.ifaceIdx = i
			}
			hasMarker = true
		case "parent":
			cols.parentIdxs = append(cols.parentIdxs, i)
			hasMarker = true
		case "device", "virtual machine", "virtual_machine":
			fallbackParents = append(fallbackParents, i)
			hasMarker = true
		case "vrf", "tenant", "role":
			hasMarker = true
		}
	}
	cols.parentIdxs = append(cols.parentIdxs, fallbackParents...)
	return cols, cols.ipIdx >= 0 && hasMarker
}

func parseNetBoxCSV(path, kind string, data []byte) ([]Entry, error) {
	trimmed, lineOffset := trimLeadingCommentsAndBlanks(data)
	if len(trimmed) == 0 {
		return []Entry{}, nil
	}
	r := csv.NewReader(bytes.NewReader(trimmed))
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, formatCSVError(path, kind, lineOffset, err)
	}
	cols, ok := parseNetBoxHeader(header)
	if !ok {
		return nil, fmt.Errorf("%s %q: missing NetBox IP Address header", kind, path)
	}

	var rows []netBoxRow
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, formatCSVError(path, kind, lineOffset, err)
		}
		recLine, _ := r.FieldPos(0)
		lineNo := lineOffset + recLine

		if cols.statusIdx >= 0 {
			status := csvCol(rec, cols.statusIdx)
			if status != "" && !strings.EqualFold(status, "active") {
				continue
			}
		}

		rawIP := csvCol(rec, cols.ipIdx)
		if rawIP == "" {
			return nil, fmt.Errorf("%s %q line %d: IP Address column is empty", kind, path, lineNo)
		}
		host, err := parseNetBoxIP(rawIP)
		if err != nil {
			return nil, fmt.Errorf("%s %q line %d: %w", kind, path, lineNo, err)
		}
		if len(rows)+1 > maxHostsFileTargets {
			return nil, fmt.Errorf("%s %q: expands to more than %d targets", kind, path, maxHostsFileTargets)
		}

		dnsName := csvCol(rec, cols.dnsIdx)
		var parent string
		for _, idx := range cols.parentIdxs {
			if v := csvCol(rec, idx); v != "" {
				parent = v
				break
			}
		}
		baseName := dnsName
		if baseName == "" {
			baseName = parent
		}
		iface := csvCol(rec, cols.ifaceIdx)

		rows = append(rows, netBoxRow{
			host:     host,
			baseName: baseName,
			iface:    iface,
		})
	}
	out := resolveNetBoxNames(rows, nil)
	for i := range out {
		out[i].netBox = &rows[i]
	}
	return out, nil
}

func csvCol(rec []string, idx int) string {
	if idx < 0 || idx >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[idx])
}

func parseNetBoxIP(s string) (string, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return "", errors.New("invalid IP address in IP Address column")
		}
		return p.Addr().String(), nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return "", errors.New("invalid IP address in IP Address column")
	}
	return addr.String(), nil
}

func formatCSVError(path, kind string, lineOffset int, err error) error {
	if pe, ok := errors.AsType[*csv.ParseError](err); ok {
		return fmt.Errorf("%s %q line %d: %w", kind, path, lineOffset+pe.Line, pe.Err)
	}
	return fmt.Errorf("%s %q: %w", kind, path, err)
}

// resolveNetBoxNames assigns display names to NetBox rows, preferring DNS name
// over Parent and disambiguating duplicates with the interface name and/or IP
// address so every non-empty Name is unique and never collides with another
// row's Host or an explicitly reserved name or host.
func resolveNetBoxNames(rows []netBoxRow, reserved []Entry) []Entry {
	if len(rows) == 0 {
		return []Entry{}
	}
	hostCount := make(map[string]int, len(rows))
	baseCount := make(map[string]int, len(rows))
	reservedNames := make(map[string]bool, len(reserved))
	for _, h := range reserved {
		hostCount[h.Host]++
		if h.Name != "" {
			reservedNames[h.Name] = true
		}
	}
	for _, r := range rows {
		hostCount[r.host]++
		if r.baseName != "" {
			baseCount[r.baseName]++
		}
	}
	collidesWithIdentity := func(name, ownHost string) bool {
		n := hostCount[name]
		if name == ownHost {
			n--
		}
		return n > 0 || reservedNames[name]
	}

	cand := make([]string, len(rows))
	for i, r := range rows {
		if r.baseName == "" {
			continue
		}
		if baseCount[r.baseName] == 1 && !collidesWithIdentity(r.baseName, r.host) {
			cand[i] = r.baseName
			continue
		}
		if r.iface != "" {
			cand[i] = fmt.Sprintf("%s (%s)", r.baseName, r.iface)
		} else {
			cand[i] = fmt.Sprintf("%s (%s)", r.baseName, r.host)
		}
	}

	candCount := make(map[string]int, len(rows))
	for _, c := range cand {
		if c != "" {
			candCount[c]++
		}
	}

	out := make([]Entry, len(rows))
	seen := make(map[string]int, len(rows))
	for i, r := range rows {
		name := cand[i]
		if name != "" && (candCount[name] > 1 || collidesWithIdentity(name, r.host)) {
			if r.iface != "" {
				name = fmt.Sprintf("%s (%s %s)", r.baseName, r.iface, r.host)
			} else {
				name = fmt.Sprintf("%s (%s)", r.baseName, r.host)
			}
		}
		if name != "" {
			seen[name]++
			if seen[name] > 1 || collidesWithIdentity(name, r.host) {
				base := name
				for k := max(2, seen[name]); ; k++ {
					try := fmt.Sprintf("%s #%d", base, k)
					if seen[try] == 0 && !collidesWithIdentity(try, r.host) {
						name = try
						seen[name] = 1
						break
					}
				}
			}
		}
		out[i] = Entry{Host: r.host, Name: name}
	}
	return out
}

// resolveNetBoxDocNames resolves generated names across all lists, reserving
// explicit names and hosts without changing user-supplied display names.
func resolveNetBoxDocNames(doc *Document) {
	var entries []*Entry
	var rows []netBoxRow
	var reserved []Entry
	collect := func(hosts []Entry) {
		for i := range hosts {
			h := &hosts[i]
			if h.netBox == nil {
				reserved = append(reserved, *h)
				continue
			}
			entries = append(entries, h)
			rows = append(rows, *h.netBox)
		}
	}
	collect(doc.Hosts)
	for _, g := range doc.Groups {
		collect(g.Hosts)
	}
	for i, h := range resolveNetBoxNames(rows, reserved) {
		entries[i].Name = h.Name
		entries[i].netBox = nil
	}
}
