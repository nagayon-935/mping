package pinger

import (
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

func icmpErrorString(typ icmp.Type, code int) string {
	switch typ {
	case ipv4.ICMPTypeDestinationUnreachable:
		return destUnreachString(code)
	case ipv4.ICMPTypeTimeExceeded:
		return timeExceededString(code)
	case ipv4.ICMPTypeParameterProblem:
		return paramProblemString(code)
	default:
		return "ICMP Error"
	}
}

func icmpV6ErrorString(typ icmp.Type, code int) string {
	switch typ {
	case ipv6.ICMPTypeDestinationUnreachable:
		return destUnreachV6String(code)
	case ipv6.ICMPTypeTimeExceeded:
		return "Time Exceeded"
	case ipv6.ICMPTypeParameterProblem:
		return "Parameter Problem"
	default:
		return "ICMPv6 Error"
	}
}

var destUnreachCodes = map[int]string{
	0:  "Destination Network Unreachable",
	1:  "Destination Host Unreachable",
	2:  "Destination Protocol Unreachable",
	3:  "Destination Port Unreachable",
	4:  "Fragmentation Needed",
	5:  "Source Route Failed",
	6:  "Destination Network Unknown",
	7:  "Destination Host Unknown",
	8:  "Source Host Isolated",
	9:  "Network Administratively Prohibited",
	10: "Host Administratively Prohibited",
	11: "Network Unreachable for ToS",
	12: "Host Unreachable for ToS",
	13: "Communication Administratively Prohibited",
	14: "Host Precedence Violation",
	15: "Precedence Cutoff in Effect",
}

var destUnreachV6Codes = map[int]string{
	0: "No Route to Destination",
	1: "Communication with Destination Administratively Prohibited",
	3: "Address Unreachable",
	4: "Port Unreachable",
}

var timeExceededCodes = map[int]string{
	0: "Time Exceeded",
	1: "Fragment Reassembly Time Exceeded",
}

var paramProblemCodes = map[int]string{
	0: "Parameter Problem",
	1: "Missing Required Option",
	2: "Bad Length",
}

func lookupICMPCode(codes map[int]string, code int, fallback string) string {
	if s, ok := codes[code]; ok {
		return s
	}
	return fallback
}

func destUnreachString(code int) string {
	return lookupICMPCode(destUnreachCodes, code, "Destination Unreachable")
}

func destUnreachV6String(code int) string {
	return lookupICMPCode(destUnreachV6Codes, code, "Destination Unreachable")
}

func timeExceededString(code int) string {
	return lookupICMPCode(timeExceededCodes, code, "Time Exceeded")
}

func paramProblemString(code int) string {
	return lookupICMPCode(paramProblemCodes, code, "Parameter Problem")
}

// extractEchoIDSeq extracts the ICMP echo ID and sequence number from an ICMP
// error message (Time Exceeded, Destination Unreachable, Parameter Problem).
// It first tries parsing the embedded original IP+ICMP headers, then falls back
// to scanning for the traceSignature pattern in the payload.
// icmpErrorBodyData extracts the embedded original packet data from an ICMP error body.
func icmpErrorBodyData(msg *icmp.Message) ([]byte, bool) {
	switch body := msg.Body.(type) {
	case *icmp.DstUnreach:
		return body.Data, true
	case *icmp.TimeExceeded:
		return body.Data, true
	case *icmp.ParamProb:
		return body.Data, true
	default:
		return nil, false
	}
}

func extractEchoIDSeq(msg *icmp.Message) (int, int, bool) {
	data, ok := icmpErrorBodyData(msg)
	if !ok {
		return 0, 0, false
	}

	if id, seq, ok := parseInnerEchoIDSeq(data); ok {
		return id, seq, true
	}

	// Fallback: Pattern matching for traceSignature + ID (2B) + Seq (2B)
	for i := 0; i <= len(data)-8; i++ {
		if data[i] == traceSignature[0] && data[i+1] == traceSignature[1] && data[i+2] == traceSignature[2] && data[i+3] == traceSignature[3] {
			id := int(data[i+4])<<8 | int(data[i+5])
			seq := int(data[i+6])<<8 | int(data[i+7])
			return id, seq, true
		}
	}
	return 0, 0, false
}

// parseInnerEchoIDSeq parses the embedded original packet data from an ICMP
// error body and returns the ICMP echo ID and sequence number.
func parseInnerEchoIDSeq(data []byte) (int, int, bool) {
	if len(data) < 1 {
		return 0, 0, false
	}
	version := data[0] >> 4

	if version == 4 {
		ihl := int(data[0]&0x0f) * 4
		if ihl <= 0 || len(data) < ihl {
			return 0, 0, false
		}

		// The inner packet could be ICMP (Protocol 1) or UDP (Protocol 17)
		// if we sent it via a udp4 socket.
		protocol := int(data[9])
		innerData := data[ihl:]

		if protocol == 1 { // ICMP
			inner, err := icmp.ParseMessage(1, innerData)
			if err == nil {
				if echo, ok := inner.Body.(*icmp.Echo); ok {
					return echo.ID, echo.Seq, true
				}
			}
		} else if protocol == 17 { // UDP
			// If we sent ICMP over a UDP socket (non-privileged),
			// the original packet will have a UDP header (8 bytes).
			if len(innerData) >= 8 {
				// Skip UDP header and try to parse the payload as ICMP
				inner, err := icmp.ParseMessage(1, innerData[8:])
				if err == nil {
					if echo, ok := inner.Body.(*icmp.Echo); ok {
						return echo.ID, echo.Seq, true
					}
				}
			}
		}
		return 0, 0, false
	} else if version == 6 {
		// IPv6 header is 40 bytes.
		const ipv6HeaderLen = 40
		if len(data) < ipv6HeaderLen {
			return 0, 0, false
		}

		protocol := int(data[6]) // Next Header
		innerData := data[ipv6HeaderLen:]

		if protocol == 58 { // ICMPv6
			inner, err := icmp.ParseMessage(58, innerData)
			if err == nil {
				if echo, ok := inner.Body.(*icmp.Echo); ok {
					return echo.ID, echo.Seq, true
				}
			}
		} else if protocol == 17 { // UDP
			if len(innerData) >= 8 {
				inner, err := icmp.ParseMessage(58, innerData[8:])
				if err == nil {
					if echo, ok := inner.Body.(*icmp.Echo); ok {
						return echo.ID, echo.Seq, true
					}
				}
			}
		}
	}
	return 0, 0, false
}
