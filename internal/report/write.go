package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Write publishes a complete new file without overwriting an existing path.
// Linking a closed temporary file provides atomic visibility and exclusive
// creation, including concurrent saves and symlink destinations.
func Write(path, format string, r Report) error {
	var data []byte
	var err error
	switch format {
	case "text":
		data = []byte(r.Text())
	case "json":
		data, err = json.MarshalIndent(r, "", "  ")
	default:
		return fmt.Errorf("unsupported report format %q", format)
	}
	if err != nil {
		return err
	}
	owner, err := callerOwner()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mping-report-*")
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write report: %w", err)
	}
	if err = setReportOwner(tmp, owner); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Link(tmp.Name(), path); err != nil {
		return fmt.Errorf("save report (choose a new path if it exists): %w", err)
	}
	return nil
}
