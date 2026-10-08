package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

func TestTargetSnapshotRemainsImmutableAcrossResetAndUpdates(t *testing.T) {
	target := stats.NewTargetStats("same")
	target.SetIP("192.0.2.1")
	target.IncSent()
	target.OnSuccess(2500*time.Microsecond, 64)
	port := &stats.PortCheckResult{Port: 443, Protocol: "tcp"}
	port.SetResult("Open", 3500*time.Microsecond)
	target.SetPortResults([]*stats.PortCheckResult{port})
	target.RecordEvent("test", "before capture")
	snapshot := NewTarget(target)
	target.Reset()
	target.SetIP("192.0.2.2")
	port.SetResult("Closed", 0)
	target.RecordEvent("test", "after capture")
	if snapshot.Statistics.Recv != 1 || snapshot.Statistics.LastRTTMs != 2.5 || snapshot.Statistics.IP != "192.0.2.1" || snapshot.PortDetails[0].LastRTTMS != 3.5 || snapshot.PortDetails[0].Status != "Open" {
		t.Fatalf("snapshot changed: %+v", snapshot)
	}
	current := NewTarget(target)
	if current.Statistics.Recv != 0 || !current.WindowStartedAt.After(snapshot.WindowStartedAt) || current.Statistics.ID != snapshot.Statistics.ID || current.Statistics.StartedAt != snapshot.Statistics.StartedAt {
		t.Fatal("reset did not start a new statistics window with the same identity")
	}
	r := Report{SchemaVersion: 1, Targets: []Target{snapshot}}
	text := r.Text()
	if !strings.Contains(text, "2.500") || !strings.Contains(text, "3.500") || strings.Contains(text, "after capture") {
		t.Fatalf("incorrect text snapshot: %s", text)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Targets[0].PortDetails[0].LastRTTMS != 3.5 || decoded.Targets[0].Statistics.ID != target.ID {
		t.Fatal("JSON units or identity changed")
	}
}

func TestWriteNeverOverwritesAndPublishesCompleteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	r := Report{SchemaVersion: 1, Scope: "session", Targets: []Target{}}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- Write(path, "json", r) }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful saves: %d", successes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved Report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.SchemaVersion != 1 || saved.Scope != "session" {
		t.Fatal("incomplete file")
	}
	if err := Write(path, "text", r); err == nil {
		t.Fatal("existing file overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("existing file changed")
	}
	files, err := filepath.Glob(filepath.Join(dir, ".mping-report-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files leaked: %v %v", files, err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, "json", r); err == nil {
		t.Fatal("symlink overwritten")
	}
	if err := Write(filepath.Join(dir, "missing", "report.json"), "json", r); err == nil {
		t.Fatal("missing parent accepted")
	}
	if err := Write(filepath.Join(dir, "unsupported"), "csv", r); err == nil {
		t.Fatal("unsupported format accepted")
	}
}
