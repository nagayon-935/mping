package stats

// FormatASN renders an AS number with its operator name, falling back to the
// AS number alone when the operator name is unknown.
func FormatASN(number, org string) string {
	if number == "" {
		return ""
	}
	if org == "" {
		return number
	}
	return number + " " + org
}

// FormatHop labels a route hop as "IP(ASN Org) ptr", leaving out the parts
// that are unknown; a hop with no responder is "*".
func FormatHop(ip, asn, org, ptr string) string {
	if ip == "" {
		return "*"
	}
	out := ip
	if annotation := FormatASN(asn, org); annotation != "" {
		out += "(" + annotation + ")"
	}
	if ptr != "" {
		out += " " + ptr
	}
	return out
}
