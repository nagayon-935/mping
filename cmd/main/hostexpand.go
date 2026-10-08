package main

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/nagayon-935/mping/internal/pinger"
)

// maxPatternExpansion caps how many hosts a single hosts-file pattern may
// expand to, so a typo like 10.0.0.0/8 fails loudly instead of spawning
// millions of targets. maxHostsFileTargets caps the whole file (all
// patterns and includes together) for the same reason.
const (
	maxPatternExpansion = 1024
	maxHostsFileTargets = 4096
)

var errExpansionTooLarge = errors.New("expansion too large")

// braceRangeRe matches a numeric brace range such as {1..20} or {01..12}.
var braceRangeRe = regexp.MustCompile(`\{(\d+)\.\.(\d+)\}`)

// expandHostPattern expands one hosts-file host value into concrete hosts.
// Brace ranges ({1..3}, zero padding kept) are expanded first, then each
// result may be an IPv4/IPv6 CIDR (IPv4 /30 and wider exclude the network
// and broadcast addresses) or an IPv4 range (10.0.0.1-20 for the last
// octet, 10.0.0.1-10.0.1.5 for a full range). Anything else — hostnames,
// plain addresses — passes through unchanged.
//
// Errors never quote the pattern: include files may be read with elevated
// privileges (setuid install), so callers decide whether echoing the input
// is safe.
func expandHostPattern(pattern string) ([]string, error) {
	tooLarge := func() error {
		return fmt.Errorf("expands to more than %d hosts", maxPatternExpansion)
	}
	braced, err := expandBraces(pattern)
	if errors.Is(err, errExpansionTooLarge) {
		return nil, tooLarge()
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range braced {
		if strings.ContainsAny(s, "{}") {
			return nil, errors.New("unsupported brace expression (only numeric ranges such as {1..10} are supported)")
		}
		addrs, err := expandAddressPattern(s)
		if errors.Is(err, errExpansionTooLarge) || len(out)+len(addrs) > maxPatternExpansion {
			return nil, tooLarge()
		}
		if err != nil {
			return nil, err
		}
		out = append(out, addrs...)
	}
	return out, nil
}

// expandBraces expands every {N..M} range in s, left to right, producing
// the cartesian product of all ranges.
func expandBraces(s string) ([]string, error) {
	loc := braceRangeRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return []string{s}, nil
	}
	lo, hi := s[loc[2]:loc[3]], s[loc[4]:loc[5]]
	start, errStart := strconv.Atoi(lo)
	end, errEnd := strconv.Atoi(hi)
	if errStart != nil || errEnd != nil {
		return nil, fmt.Errorf("brace range {%s..%s}: number out of range", lo, hi)
	}
	if start > end {
		return nil, fmt.Errorf("brace range {%s..%s}: start must not exceed end", lo, hi)
	}
	if end-start+1 > maxPatternExpansion {
		return nil, errExpansionTooLarge
	}
	tails, err := expandBraces(s[loc[1]:])
	if err != nil {
		return nil, err
	}
	if (end-start+1)*len(tails) > maxPatternExpansion {
		return nil, errExpansionTooLarge
	}
	width := 0
	if (len(lo) > 1 && lo[0] == '0') || (len(hi) > 1 && hi[0] == '0') {
		width = max(len(lo), len(hi))
	}
	prefix := s[:loc[0]]
	out := make([]string, 0, (end-start+1)*len(tails))
	for n := start; n <= end; n++ {
		head := prefix + fmt.Sprintf("%0*d", width, n)
		for _, tail := range tails {
			out = append(out, head+tail)
		}
	}
	return out, nil
}

// expandAddressPattern expands a CIDR or IPv4 range; any other string is
// returned as the single element of the result.
func expandAddressPattern(s string) ([]string, error) {
	if strings.Contains(s, "/") {
		return expandCIDR(s)
	}
	if i := strings.LastIndex(s, "-"); i > 0 {
		if start, err := netip.ParseAddr(s[:i]); err == nil {
			if !start.Is4() {
				return nil, errors.New("IPv6 ranges are not supported; use CIDR notation instead")
			}
			return expandIPv4Range(start, s[i+1:])
		}
	}
	return []string{s}, nil
}

func expandCIDR(s string) ([]string, error) {
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		return nil, errors.New("invalid CIDR notation (expected address/prefix-length, e.g. 192.0.2.0/28)")
	}
	prefix = prefix.Masked()
	hostBits := prefix.Addr().BitLen() - prefix.Bits()
	if hostBits > 10 { // 2^11 > maxPatternExpansion even after excluding two
		return nil, errExpansionTooLarge
	}
	addr, n := prefix.Addr(), 1<<hostBits
	if prefix.Addr().Is4() && prefix.Bits() <= 30 {
		addr, n = addr.Next(), n-2
	}
	if n > maxPatternExpansion {
		return nil, errExpansionTooLarge
	}
	out := make([]string, 0, n)
	for range n {
		out = append(out, addr.String())
		addr = addr.Next()
	}
	return out, nil
}

func expandIPv4Range(start netip.Addr, endStr string) ([]string, error) {
	var end netip.Addr
	if a, err := netip.ParseAddr(endStr); err == nil {
		if !a.Is4() {
			return nil, errors.New("invalid range: start and end must both be IPv4")
		}
		end = a
	} else if octet, ok := parseOctet(endStr); ok {
		b := start.As4()
		b[3] = octet
		end = netip.AddrFrom4(b)
	} else {
		return nil, errors.New("invalid range: end must be a last octet (0-255) or an IPv4 address")
	}
	if end.Less(start) {
		return nil, errors.New("invalid range: end must not be below start")
	}
	sb, eb := start.As4(), end.As4()
	count := uint64(ipv4ToUint32(eb)) - uint64(ipv4ToUint32(sb)) + 1
	if count > maxPatternExpansion {
		return nil, errExpansionTooLarge
	}
	out := make([]string, 0, count)
	for addr := start; ; addr = addr.Next() {
		out = append(out, addr.String())
		if addr == end {
			break
		}
	}
	return out, nil
}

// parseOctet accepts 1-3 plain decimal digits in 0-255 (no sign).
func parseOctet(s string) (byte, bool) {
	if len(s) == 0 || len(s) > 3 || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > 255 {
		return 0, false
	}
	return byte(n), true
}

func ipv4ToUint32(b [4]byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// expandHostEntries expands every YAML entry's host pattern, copying the
// entry's name and DSCP onto each result. location (e.g. "hosts" or
// `groups["Core"]`) prefixes errors together with the entry's index in the
// original, unexpanded list, so DSCP is validated here too — after
// expansion the index would no longer match what the user wrote.
func expandHostEntries(entries []hostEntry, location string) ([]hostEntry, error) {
	out := make([]hostEntry, 0, len(entries))
	for i, e := range entries {
		where := fmt.Sprintf("%s[%d]", location, i)
		if e.DSCP != "" {
			if _, err := pinger.ParseDSCP(e.DSCP); err != nil {
				return nil, fmt.Errorf("%s: dscp: %w", where, err)
			}
		}
		expanded, err := expandHostEntry(e)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", where, e.Host, err)
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// expandHostEntry expands one entry. Like expandHostPattern, its errors
// never quote the entry's text.
func expandHostEntry(e hostEntry) ([]hostEntry, error) {
	hosts, err := expandHostPattern(e.Host)
	if err != nil {
		return nil, err
	}
	if e.Name != "" && len(hosts) > 1 {
		return nil, fmt.Errorf("name cannot be used with a pattern that expands to %d hosts", len(hosts))
	}
	out := make([]hostEntry, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, hostEntry{Host: h, Name: e.Name, DSCP: e.DSCP})
	}
	return out, nil
}
