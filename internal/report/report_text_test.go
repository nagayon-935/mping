package report

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

func fullReport() Report {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	removed := now.Add(time.Minute)
	full := Target{
		Statistics:       stats.TargetSummary{Host: "example.com", IP: "192.0.2.1", ID: 1, TraceHops: []string{"a", "b"}, MTRHops: []stats.HopSummary{{TTL: 1, IP: "10.0.0.1"}}},
		WindowStartedAt:  now,
		Group:            "g",
		DSCP:             "EF",
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

func TestTextIncludesAllOptionalSections(t *testing.T) {
	out := fullReport().Text()
	for _, want := range []string{
		"Active targets:", "Removed targets (final measurements):", "5 older removed targets omitted.",
		"Independent HTTP checks:", "https://example.com", "Removed: ", "Destination IP changed 2 times",
		"1 older IP history entries omitted.", "MTR route changes 3", "Route: a -> b", "MTR hop 1 10.0.0.1",
		"Port 443/tcp Open", "4 older target events omitted.", "k: m", "All *_ms fields are milliseconds.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Text() missing %q", want)
		}
	}
}

func TestTextOmitsEmptyOptionalSections(t *testing.T) {
	out := Report{}.Text()
	for _, unwanted := range []string{"Removed targets", "Independent HTTP checks", "older removed targets"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("Text() unexpectedly contains %q", unwanted)
		}
	}
}

func TestWriteSupportsTextAndJSON(t *testing.T) {
	dir := t.TempDir()
	for format, marker := range map[string]string{"text": "mping investigation report", "json": `"schema_version"`} {
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
	if err := Write(filepath.Join(dir, "missing", "x"), "text", Report{}); err == nil || !strings.Contains(err.Error(), "create report") {
		t.Fatalf("missing dir error = %v", err)
	}
	existing := filepath.Join(dir, "exists")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(existing, "text", Report{}); err == nil || !strings.Contains(err.Error(), "choose a new path") {
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
	if err := Write(path, "text", Report{}); err == nil || !strings.Contains(err.Error(), "SUDO_UID") {
		t.Fatalf("expected owner error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("report must not be published on owner failure: %v", err)
	}
}

func TestSetReportOwnerIsNoOpForNonRoot(t *testing.T) {
	orig := geteuid
	t.Cleanup(func() { geteuid = orig })
	geteuid = func() int { return 501 }
	f, err := os.CreateTemp(t.TempDir(), "o")
	if err != nil {
		t.Fatal(err)
	}
	f.Close() // chown on a closed file would fail, proving it is never attempted
	if err := setReportOwner(f, fileOwner{uid: 1, gid: 1}); err != nil {
		t.Fatalf("setReportOwner: %v", err)
	}
}

func TestSetReportOwnerChownsAsRoot(t *testing.T) {
	orig := geteuid
	t.Cleanup(func() { geteuid = orig })
	geteuid = func() int { return 0 }
	f, err := os.CreateTemp(t.TempDir(), "o")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Chowning to the file's current owner succeeds without privileges.
	if err := setReportOwner(f, fileOwner{uid: os.Getuid(), gid: os.Getgid()}); err != nil {
		t.Fatalf("setReportOwner: %v", err)
	}
}

func TestSetReportOwnerWrapsChownFailure(t *testing.T) {
	orig := geteuid
	t.Cleanup(func() { geteuid = orig })
	geteuid = func() int { return 0 }
	f, err := os.CreateTemp(t.TempDir(), "o")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := setReportOwner(f, fileOwner{}); err == nil || !strings.Contains(err.Error(), "set report owner") {
		t.Fatalf("expected wrapped chown error, got %v", err)
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
	if err := Write(path, "text", Report{}); err == nil || !strings.Contains(err.Error(), "set report owner") {
		t.Fatalf("expected chown error, got %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected empty dir, found %d entries", len(entries))
	}
}
