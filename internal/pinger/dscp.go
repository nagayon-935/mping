package pinger

import (
	"fmt"
	"strconv"
	"strings"
)

// dscpUnset marks a DSCP-derived TOS/TrafficClass field as "not configured":
// -1 is outside the valid 0-255 byte range a real TOS/TrafficClass value
// can take, so it can't be confused with an explicit "0" (CS0/Default)
// selection the way a bare zero value would be.
const dscpUnset = -1

// dscpNameTable maps the DSCP codepoint names Apple's ping.c/ping6.c accept
// via -z tos / -z tclass (str2tos/str2tclass share one identical table) to
// their 6-bit DSCP codepoint value (0-63). Keys are matched case-
// insensitively by ParseDSCP.
var dscpNameTable = map[string]int{
	"DF": 0,  // Default Forwarding (RFC 8622) == CS0
	"EF": 46, // Expedited Forwarding (RFC 3246)
	"VA": 44, // Voice-Admit (RFC 5865)

	"CS0": 0, "CS1": 8, "CS2": 16, "CS3": 24,
	"CS4": 32, "CS5": 40, "CS6": 48, "CS7": 56,

	"AF11": 10, "AF12": 12, "AF13": 14,
	"AF21": 18, "AF22": 20, "AF23": 22,
	"AF31": 26, "AF32": 28, "AF33": 30,
	"AF41": 34, "AF42": 36, "AF43": 38,
}

// ParseDSCP parses a DSCP specification — either one of dscpNameTable's
// codepoint names (case-insensitive, e.g. "ef", "AF41") or a bare decimal
// number — and returns the byte value to place directly in the IPv4 TOS /
// IPv6 TrafficClass field via ipv4.ControlMessage.TOS / (*ipv4.PacketConn).
// SetTOS or their IPv6 TrafficClass equivalents.
//
// A named codepoint is looked up as a 6-bit DSCP value and shifted left 2
// bits, since DSCP occupies only the top 6 bits of the 8-bit TOS/
// TrafficClass field (the low 2 bits are ECN) — mirroring str2tos/
// str2tclass. A bare numeric value (0-255) is used as the final field byte
// directly, with no shift, matching str2tos's numeric path: it lets a
// caller specify the full byte (DSCP and ECN bits together) when the named
// table isn't precise enough.
func ParseDSCP(s string) (int, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("invalid DSCP value %q: empty", s)
	}

	if dscp, ok := dscpNameTable[strings.ToUpper(trimmed)]; ok {
		return dscp << 2, nil
	}

	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid DSCP value %q: not a known name or number", s)
	}
	if n < 0 || n > 255 {
		return 0, fmt.Errorf("invalid DSCP value %d: must be 0-255", n)
	}
	return n, nil
}

// dscpValueNames is dscpNameTable's reverse lookup (DSCP codepoint -> name),
// used by DSCPName to render an observed TOS/TrafficClass byte back into a
// human-readable codepoint. Built as an explicit literal rather than derived
// from dscpNameTable so the DF/CS0 alias (both value 0) has one deterministic
// winner: CS0 is the more diagnostic name for "unmarked/best-effort" traffic
// on a received packet, where DF's RFC 8622 low-latency connotation doesn't
// apply.
var dscpValueNames = map[int]string{
	46: "EF",
	44: "VA",
	0:  "CS0",
	8:  "CS1", 16: "CS2", 24: "CS3",
	32: "CS4", 40: "CS5", 48: "CS6", 56: "CS7",
	10: "AF11", 12: "AF12", 14: "AF13",
	18: "AF21", 20: "AF22", 22: "AF23",
	26: "AF31", 28: "AF32", 30: "AF33",
	34: "AF41", 36: "AF42", 38: "AF43",
}

// DSCPName formats a TOS (IPv4) / TrafficClass (IPv6) byte for display: the
// top 6 bits (the DSCP codepoint) are matched against dscpValueNames; the
// low 2 bits (ECN) are ignored, since a router may rewrite ECN independently
// of DSCP and a name lookup that required an exact byte match would silently
// stop recognizing EF/CS0/etc. the moment ECN got touched anywhere on path.
// Falls back to the bare decimal DSCP codepoint (0-63) when it doesn't match
// a known name — still meaningful for spotting re-marking/bleaching even
// without a name.
func DSCPName(tos int) string {
	dscp := (tos >> 2) & 0x3f
	if name, ok := dscpValueNames[dscp]; ok {
		return name
	}
	return strconv.Itoa(dscp)
}
