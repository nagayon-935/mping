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
