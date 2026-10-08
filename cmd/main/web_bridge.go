package main

import (
	"fmt"
	"io"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/web"
)

// webStart is a seam for tests, mirroring uiRun.
var webStart = web.Start

// startWebUI starts the dashboard when --web is set, returning a nil server
// when it is off. ok=false means it was requested but could not start; the
// error has already been written to errOut.
func startWebUI(cfg config, src *web.Source, errOut io.Writer) (srv *web.Server, ok bool) {
	if !cfg.webEnabled {
		return nil, true
	}
	srv, err := webStart(web.Options{Port: cfg.webPort, Source: src})
	if err != nil {
		fmt.Fprintf(errOut, "Error starting web UI: %v\n", err)
		return nil, false
	}
	return srv, true
}

// closeWebUI stops srv (nil-safe). A shutdown error only means a stream
// had to be cut off; it never changes mping's exit status.
func closeWebUI(srv *web.Server, errOut io.Writer) {
	if srv == nil {
		return
	}
	if err := srv.Close(); err != nil {
		fmt.Fprintf(errOut, "Warning: %v\n", err)
	}
}

// webProvider adapts one run-loop iteration's supervisor to web.Provider.
type webProvider struct {
	targets     func() ui.TargetSet
	httpResults func() []*stats.HTTPCheckResult
	base        web.Meta
}

func newWebProvider(sup *supervisor, cfg config, hosts []targetSpec, portCount int) webProvider {
	return webProvider{
		targets:     sup.liveTargets,
		httpResults: sup.httpResults,
		base:        webMeta(cfg, hosts, portCount),
	}
}

func (p webProvider) Targets() []*stats.TargetStats         { return p.targets().Targets }
func (p webProvider) HTTPResults() []*stats.HTTPCheckResult { return p.httpResults() }

// Meta re-derives groups on every call: hosts added or deleted at runtime
// change which target IDs each group holds.
func (p webProvider) Meta() web.Meta {
	m := p.base
	m.Groups = webGroups(p.targets())
	return m
}

// webMeta captures the per-iteration settings the browser renders with;
// portCount is the number of parsed --port specs (env.portSpecs).
func webMeta(cfg config, hosts []targetSpec, portCount int) web.Meta {
	th := cfg.thresholds
	return web.Meta{
		IntervalMs: float64(cfg.intervalMs),
		Features: web.Features{
			Traceroute: cfg.trace,
			MTR:        cfg.mtr,
			Port:       portCount > 0,
			HTTP:       len(cfg.httpURLs) > 0,
			ASN:        cfg.asnEnabled,
			PTR:        cfg.ptrEnabled,
			DSCP:       dscpColumnEnabled(cfg, hosts),
		},
		Thresholds: web.Thresholds{
			RTTWarnMs:    durationMs(th.RTTWarn),
			RTTCritMs:    durationMs(th.RTTCrit),
			JitterWarnMs: durationMs(th.JitterWarn),
			JitterCritMs: durationMs(th.JitterCrit),
			LossWarnPct:  th.LossWarn,
			LossCritPct:  th.LossCrit,
		},
	}
}

// webGroups converts the UI's index-based groups to ID-based ones, dropping
// indices that no longer point at a live target.
func webGroups(set ui.TargetSet) []web.Group {
	groups := make([]web.Group, 0, len(set.Groups))
	for _, g := range set.Groups {
		ids := make([]uint64, 0, len(g.Indices))
		for _, i := range g.Indices {
			if i >= 0 && i < len(set.Targets) {
				ids = append(ids, set.Targets[i].ID)
			}
		}
		groups = append(groups, web.Group{Name: g.Name, TargetIDs: ids})
	}
	return groups
}

func durationMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
