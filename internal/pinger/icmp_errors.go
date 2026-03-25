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

func destUnreachString(code int) string {
	switch code {
	case 0:
		return "Destination Network Unreachable"
	case 1:
		return "Destination Host Unreachable"
	case 2:
		return "Destination Protocol Unreachable"
	case 3:
		return "Destination Port Unreachable"
	case 4:
		return "Fragmentation Needed"
	case 5:
		return "Source Route Failed"
	case 6:
		return "Destination Network Unknown"
	case 7:
		return "Destination Host Unknown"
	case 8:
		return "Source Host Isolated"
	case 9:
		return "Network Administratively Prohibited"
	case 10:
		return "Host Administratively Prohibited"
	case 11:
		return "Network Unreachable for ToS"
	case 12:
		return "Host Unreachable for ToS"
	case 13:
		return "Communication Administratively Prohibited"
	case 14:
		return "Host Precedence Violation"
	case 15:
		return "Precedence Cutoff in Effect"
	default:
		return "Destination Unreachable"
	}
}

func destUnreachV6String(code int) string {
	switch code {
	case 0:
		return "No Route to Destination"
	case 1:
		return "Communication with Destination Administratively Prohibited"
	case 3:
		return "Address Unreachable"
	case 4:
		return "Port Unreachable"
	default:
		return "Destination Unreachable"
	}
}

func timeExceededString(code int) string {
	switch code {
	case 0:
		return "Time Exceeded"
	case 1:
		return "Fragment Reassembly Time Exceeded"
	default:
		return "Time Exceeded"
	}
}

func paramProblemString(code int) string {
	switch code {
	case 0:
		return "Parameter Problem"
	case 1:
		return "Missing Required Option"
	case 2:
		return "Bad Length"
	default:
		return "Parameter Problem"
	}
}

// extractEchoIDSeq extracts the ICMP echo ID and sequence number from an ICMP
// error message (Time Exceeded, Destination Unreachable, Parameter Problem).
// It first tries parsing the embedded original IP+ICMP headers, then falls back
// to scanning for the traceSignature pattern in the payload.
func extractEchoIDSeq(msg *icmp.Message) (int, int, bool) {
	var data []byte
	switch body := msg.Body.(type) {
	case *icmp.DstUnreach:
		id, seq, ok := parseInnerEchoIDSeq(body.Data)
		if ok {
			return id, seq, ok
		}
		data = body.Data
	case *icmp.TimeExceeded:
		id, seq, ok := parseInnerEchoIDSeq(body.Data)
		if ok {
			return id, seq, ok
		}
		data = body.Data
	case *icmp.ParamProb:
		id, seq, ok := parseInnerEchoIDSeq(body.Data)
		if ok {
			return id, seq, ok
		}
		data = body.Data
	default:
		return 0, 0, false
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
