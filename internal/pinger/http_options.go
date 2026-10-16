package pinger

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type StatusRange struct{ Min, Max int }
type HTTPOptions struct {
	ExpectedStatus  []StatusRange
	FollowRedirects bool
	Headers         http.Header
}

func DefaultHTTPOptions() HTTPOptions {
	return HTTPOptions{ExpectedStatus: []StatusRange{{200, 399}}, FollowRedirects: true}
}
func (o HTTPOptions) Clone() HTTPOptions {
	o.ExpectedStatus = append([]StatusRange(nil), o.ExpectedStatus...)
	o.Headers = o.Headers.Clone()
	return o
}
func (o HTTPOptions) Accepts(code int) bool {
	for _, r := range o.ExpectedStatus {
		if code >= r.Min && code <= r.Max {
			return true
		}
	}
	return false
}
func ParseHTTPStatus(raw string) ([]StatusRange, error) {
	var ranges []StatusRange
	for _, part := range strings.Split(raw, ",") {
		lo, hi, span := strings.Cut(strings.TrimSpace(part), "-")
		min, err := strconv.Atoi(lo)
		if err != nil || min < 100 || min > 599 {
			return nil, fmt.Errorf("http-status: expected codes/ranges between 100 and 599")
		}
		max := min
		if span {
			max, err = strconv.Atoi(hi)
			if err != nil || max < min || max > 599 {
				return nil, fmt.Errorf("http-status: invalid or reversed range")
			}
		}
		ranges = append(ranges, StatusRange{min, max})
	}
	return ranges, nil
}
