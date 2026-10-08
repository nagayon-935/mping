package web

import (
	"math"
	"math/rand/v2"
	"os"
	"os/signal"
	"strconv"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// TestDevServer serves the dashboard over simulated targets so the UI can be
// inspected in a browser without raw-socket privileges. It is skipped unless
// MPING_WEB_DEV_PORT is set:
//
//	MPING_WEB_DEV_PORT=8090 go test -run TestDevServer -timeout 0 ./internal/web
//
// It runs until interrupted (Ctrl-C).
func TestDevServer(t *testing.T) {
	raw := os.Getenv("MPING_WEB_DEV_PORT")
	if raw == "" {
		t.Skip("set MPING_WEB_DEV_PORT to run the simulated dashboard")
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("MPING_WEB_DEV_PORT: %v", err)
	}

	sim := newSimulation()
	src := NewSource()
	src.Set(sim)
	s, err := Start(Options{Port: port, Source: src})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t.Logf("dashboard: %s", s.URL())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for step := 0; ; step++ {
		select {
		case <-stop:
			src.MarkStopped()
			return
		case <-tick.C:
			sim.step(step)
		}
	}
}

type simTarget struct {
	ts       *stats.TargetStats
	baseMs   float64
	jitterMs float64
	lossProb float64
}

type simulation struct {
	targets []simTarget
	http    []*stats.HTTPCheckResult
}

func newSimulation() *simulation {
	mk := func(host, ip, ptr string, base, jitter, loss float64) simTarget {
		ts := stats.NewTargetStats(host)
		ts.SetIP(ip)
		if ptr != "" {
			ts.SetPTR(ptr)
		}
		ts.SetASNInfo("AS64500", "JP", "Example Networks")
		return simTarget{ts: ts, baseMs: base, jitterMs: jitter, lossProb: loss}
	}
	sim := &simulation{targets: []simTarget{
		mk("core-rtr1.example", "192.0.2.1", "core-rtr1.lab.example", 1.2, 0.3, 0),
		mk("core-rtr2.example", "192.0.2.2", "", 2.5, 0.6, 0.01),
		mk("cdn.example", "198.51.100.10", "edge-nrt.cdn.example", 70, 25, 0.02),
		mk("flaky.example", "198.51.100.20", "", 35, 8, 0.3),
		mk("down.example", "203.0.113.9", "", 0, 0, 1),
		mk("<script>alert(1)</script>", "2001:db8::1", "\"><img src=x onerror=alert(1)>", 12, 2, 0),
	}}
	hops := []string{"192.0.2.254", "198.51.100.1", "", "198.51.100.10"}
	sim.targets[2].ts.SetTraceHops(hops)
	m := sim.targets[2].ts.MTR()
	m.EnsureLen(len(hops))
	ports := []*stats.PortCheckResult{{Port: 443, Protocol: "tcp"}, {Port: 53, Protocol: "udp"}}
	ports[0].SetResult("Open", 4*time.Millisecond)
	ports[1].SetResult("Filtered", 0)
	sim.targets[0].ts.SetPortResults(ports)
	hc := stats.NewHTTPCheckResult("https://status.example/health")
	hc.SetResult(200, 42*time.Millisecond, nil)
	sim.http = []*stats.HTTPCheckResult{hc}
	return sim
}

func (s *simulation) step(n int) {
	for i, st := range s.targets {
		st.ts.IncSent()
		if rand.Float64() < st.lossProb {
			st.ts.OnFailure("Request timeout")
			continue
		}
		wave := math.Sin(float64(n+i*7)/20) * st.jitterMs
		ms := math.Max(0.05, st.baseMs+wave+rand.NormFloat64()*st.jitterMs/3)
		st.ts.OnSuccess(time.Duration(ms*float64(time.Millisecond)), 57)
	}
	m := s.targets[2].ts.MTR()
	for ttl, ip := range []string{"192.0.2.254", "198.51.100.1", "", "198.51.100.10"} {
		if ip == "" {
			m.RecordLoss(ttl + 1)
			continue
		}
		m.RecordReply(ttl+1, ip, "AS64500", "JP", "Example Networks", time.Duration((5+ttl*20)*int(time.Millisecond)))
	}
	if n%40 == 0 {
		s.targets[3].ts.RecordEvent("route", "simulated route change")
	}
}

func (s *simulation) Targets() []*stats.TargetStats {
	out := make([]*stats.TargetStats, len(s.targets))
	for i, st := range s.targets {
		out[i] = st.ts
	}
	return out
}

func (s *simulation) HTTPResults() []*stats.HTTPCheckResult { return s.http }

func (s *simulation) Meta() Meta {
	ids := func(idx ...int) []uint64 {
		out := make([]uint64, len(idx))
		for i, j := range idx {
			out[i] = s.targets[j].ts.ID
		}
		return out
	}
	return Meta{
		IntervalMs: 250,
		Features:   Features{MTR: true, Port: true, HTTP: true, ASN: true, PTR: true},
		Thresholds: Thresholds{RTTWarnMs: 50, RTTCritMs: 200, JitterWarnMs: 10, JitterCritMs: 50, LossWarnPct: 20, LossCritPct: 80},
		Groups: []Group{
			{Name: "core", TargetIDs: ids(0, 1)},
			{Name: "internet", TargetIDs: ids(2, 3, 4)},
		},
	}
}
