package report

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestWriteWrapsFinalizationErrors(t *testing.T) {
	for _, tt := range []struct {
		operation string
		wantCalls []string
	}{
		{"sync", []string{"sync"}},
		{"close", []string{"sync", "close"}},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			operation := tt.operation
			origSync, origClose := syncReportFile, closeReportFile
			t.Cleanup(func() { syncReportFile, closeReportFile = origSync, origClose })
			cause := &os.PathError{Op: operation, Path: "temporary report", Err: errors.New("I/O failure")}
			var tmp *os.File
			var calls []string
			syncReportFile = func(f *os.File) error {
				tmp = f
				calls = append(calls, "sync")
				if operation == "sync" {
					return cause
				}
				return origSync(f)
			}
			closeReportFile = func(f *os.File) error {
				calls = append(calls, "close")
				if err := origClose(f); err != nil {
					t.Fatal(err)
				}
				return cause
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "report.json")
			err := Write(path, "json", Report{SchemaVersion: 1})
			want := operation + " report: " + cause.Error()
			if err == nil || err.Error() != want {
				t.Errorf("Write error = %v, want %q", err, want)
			}
			if !errors.Is(err, cause) {
				t.Errorf("Write error = %v, want wrapped cause %v", err, cause)
			}
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || pathErr != cause {
				t.Errorf("Write error = %v, want underlying PathError %v", err, cause)
			}
			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("finalization calls = %v, want %v", calls, tt.wantCalls)
			}
			if tmp == nil {
				t.Fatal("temporary report was not synced")
			}
			if _, err := tmp.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Errorf("temporary report is not closed: %v", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("report published after %s failure: %v", operation, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Errorf("temporary report leaked: entries=%v err=%v", entries, err)
			}
		})
	}
}
