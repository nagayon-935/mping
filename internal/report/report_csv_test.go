package report

import (
	"bytes"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

func fullReport() Report {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	removed := now.Add(time.Minute)
	full := Target{
		Statistics:       stats.TargetSummary{Host: "example.com", IP: "192.0.2.1", ID: 1, MTRHops: []stats.HopSummary{{TTL: 1, IP: "10.0.0.1", PTR: "gw.example"}, {TTL: 2}}, MTRError: "socket: permission denied"},
		WindowStartedAt:  now,
		Group:            "g",
		IPChanges:        2,
		IPHistoryDropped: 1,
		IPHistory:        []stats.IPChange{{At: now, IP: "192.0.2.1"}},
		MTRFlapCount:     3,
		MTRLastFlapAt:    now,
		MTRLastFlapDesc:  "route changed",
		PortDetails:      []Port{{Port: 443, Protocol: "tcp", Status: "Open", LastChange: now}},
		Events:           []stats.Event{{At: now, Kind: "k", Message: "m"}},
		EventsDropped:    4,
	}
	gone := full
	gone.RemovedAt = &removed
	return Report{
		SchemaVersion: 1, SessionStartedAt: now, CollectionStartedAt: now, CapturedAt: now, CaptureCompletedAt: now,
		State: "running", Scope: "all",
		Targets:               []Target{full},
		RemovedTargets:        []Target{gone},
		RemovedTargetsDropped: 5,
		HTTPChecks:            []stats.HTTPCheckSummary{{URL: "https://example.com", Status: "UP", StatusCode: 200}},
	}
}

func TestCSVHasOneRowPerTargetIncludingRemovedOnes(t *testing.T) {
	r := fullReport()
	r.Targets[0].Statistics.Sent, r.Targets[0].Statistics.Recv = 10, 9
	r.Targets[0].Statistics.LastRTTMs, r.Targets[0].Statistics.AvgRTTMs = 12.3456, 10
	data, err := r.CSV()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("\ufeff")) {
		t.Fatal("CSV lacks the UTF-8 byte order mark")
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\ufeff")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || !slices.Equal(rows[0], csvHeader) {
		t.Fatalf("rows = %q, want the header, one active and one removed target", rows)
	}
	col := func(row []string, name string) string { return row[slices.Index(csvHeader, name)] }
	active, removed := rows[1], rows[2]
	for name, want := range map[string]string{
		"captured_at": "2026-01-02T03:04:05Z", "target_id": "1", "host": "example.com", "group": "g",
		"removed_at": "", "sent": "10", "received": "9", "last_rtt_ms": "12.346", "avg_rtt_ms": "10.000",
		"route_changes": "3", "statistics_since": "2026-01-02T03:04:05Z",
	} {
		if got := col(active, name); got != want {
			t.Errorf("active %s = %q, want %q", name, got, want)
		}
	}
	if got := col(removed, "removed_at"); got != "2026-01-02T03:05:05Z" {
		t.Errorf("removed_at = %q", got)
	}
	// No reply yet: RTT and TTL are blank rather than zero.
	if got := col(removed, "avg_rtt_ms") + col(removed, "last_ttl"); got != "" {
		t.Errorf("RTT/TTL without replies = %q, want blank", got)
	}
}

func TestCSVKeepsTextCellsFromBecomingFormulas(t *testing.T) {
	r := Report{Targets: []Target{{Statistics: stats.TargetSummary{Host: "=HYPERLINK(\"x\")", Org: "+evil", PTR: "-1", LastError: "@x"}}}}
	data, err := r.CSV()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\ufeff")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"host", "org", "ptr", "last_error"} {
		if got := rows[1][slices.Index(csvHeader, name)]; !strings.HasPrefix(got, "'") {
			t.Errorf("%s = %q, want a leading quote", name, got)
		}
	}
}

func TestWriteSupportsJSONAndCSV(t *testing.T) {
	dir := t.TempDir()
	for format, marker := range map[string]string{"csv": "captured_at,target_id", "json": `"schema_version"`} {
		path := filepath.Join(dir, "r."+format)
		if err := Write(path, format, fullReport()); err != nil {
			t.Fatalf("Write(%s): %v", format, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), marker) {
			t.Fatalf("%s report missing %q (err=%v)", format, marker, err)
		}
	}
}

func TestWriteRejectsInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	if err := Write(filepath.Join(dir, "x"), "xml", Report{}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported format error = %v", err)
	}
	if err := Write(filepath.Join(dir, "missing", "x"), "csv", Report{}); err == nil || !strings.Contains(err.Error(), "create report") {
		t.Fatalf("missing dir error = %v", err)
	}
	existing := filepath.Join(dir, "exists")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(existing, "csv", Report{}); err == nil || !strings.Contains(err.Error(), "choose a new path") {
		t.Fatalf("existing path error = %v", err)
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep" {
		t.Fatalf("existing file overwritten: %q", data)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".mping-report-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestWriteFailsWhenOwnerCannotBeDetermined(t *testing.T) {
	orig := callerOwner
	t.Cleanup(func() { callerOwner = orig })
	callerOwner = func() (fileOwner, error) { return fileOwner{}, errors.New("invalid SUDO_UID") }
	path := filepath.Join(t.TempDir(), "x")
	if err := Write(path, "csv", Report{}); err == nil || !strings.Contains(err.Error(), "SUDO_UID") {
		t.Fatalf("expected owner error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("report must not be published on owner failure: %v", err)
	}
}

func TestSetReportOwner(t *testing.T) {
	tests := []struct {
		name    string
		euid    int
		closed  bool // chown on a closed file fails, so success proves chown was never attempted
		owner   fileOwner
		wantErr string // empty: no error expected
	}{
		{name: "no-op for non-root", euid: 501, closed: true, owner: fileOwner{uid: 1, gid: 1}},
		// Chowning to the file's current owner succeeds without privileges.
		{name: "chowns as root", euid: 0, owner: fileOwner{uid: os.Getuid(), gid: os.Getgid()}},
		{name: "wraps chown failure", euid: 0, closed: true, wantErr: "set report owner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := geteuid
			t.Cleanup(func() { geteuid = orig })
			geteuid = func() int { return tt.euid }
			f, err := os.CreateTemp(t.TempDir(), "o")
			if err != nil {
				t.Fatal(err)
			}
			if tt.closed {
				f.Close()
			} else {
				defer f.Close()
			}

			err = setReportOwner(f, tt.owner)

			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("setReportOwner: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("setReportOwner error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestWriteRemovesTempFileWhenOwnerChownFails(t *testing.T) {
	origE, origO := geteuid, callerOwner
	t.Cleanup(func() { geteuid, callerOwner = origE, origO })
	geteuid = func() int { return 0 }
	// uid 0 chown fails for non-root; as real root it succeeds, so skip there.
	if os.Getuid() == 0 {
		t.Skip("chown to root succeeds when running as root")
	}
	callerOwner = func() (fileOwner, error) { return fileOwner{uid: 0, gid: 0}, nil }
	dir := t.TempDir()
	path := filepath.Join(dir, "x")
	if err := Write(path, "csv", Report{}); err == nil || !strings.Contains(err.Error(), "set report owner") {
		t.Fatalf("expected chown error, got %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected empty dir, found %d entries", len(entries))
	}
}
