package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Write publishes a complete new file without overwriting an existing path.
// Linking a closed temporary file provides atomic visibility and exclusive
// creation, including concurrent saves and symlink destinations.
func Write(path, format string, r Report) error {
	var data []byte
	var err error
	switch format {
	case "csv":
		data, err = r.CSV()
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
		return fmt.Errorf("sync report: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close report: %w", err)
	}
	if err = os.Link(tmp.Name(), path); err != nil {
		return fmt.Errorf("save report (choose a new path if it exists): %w", err)
	}
	return nil
}

// PathFormat infers the report format and supplies .json when no extension
// was entered. Unknown extensions are rejected instead of silently changing
// the meaning of an explicitly named file.
func PathFormat(path string) (string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", fmt.Errorf("enter a file path")
	}
	if strings.HasSuffix(path, string(filepath.Separator)) || filepath.Base(path) == "." || filepath.Base(path) == ".." {
		return "", "", fmt.Errorf("enter a file name ending in .json or .csv")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return path, "json", nil
	case ".csv":
		return path, "csv", nil
	case "":
		return path + ".json", "json", nil
	default:
		return "", "", fmt.Errorf("use a .json or .csv file name")
	}
}

type fileOwner struct{ uid, gid int }

// ownerForCaller uses the real IDs to identify the caller in setuid installations.
// sudo sets both real and effective UID to root, so use its original caller
// IDs only in that case. Ordinary users' environment variables are never consulted.
func ownerForCaller(uid, gid, euid int, getenv func(string) string) (fileOwner, error) {
	owner := fileOwner{uid: uid, gid: gid}
	if uid != 0 || euid != 0 {
		return owner, nil
	}
	sudoUID, sudoGID := getenv("SUDO_UID"), getenv("SUDO_GID")
	if sudoUID == "" && sudoGID == "" {
		return owner, nil
	}
	parse := func(name, value string) (int, error) {
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil || id == 1<<32-1 {
			return 0, fmt.Errorf("invalid %s: cannot determine report owner", name)
		}
		return int(id), nil
	}
	var err error
	if owner.uid, err = parse("SUDO_UID", sudoUID); err != nil {
		return fileOwner{}, err
	}
	if owner.gid, err = parse("SUDO_GID", sudoGID); err != nil {
		return fileOwner{}, err
	}
	return owner, nil
}

// geteuid and callerOwner are swappable in tests so root-only paths need no privileges.
var (
	geteuid     = os.Geteuid
	callerOwner = func() (fileOwner, error) {
		return ownerForCaller(os.Getuid(), os.Getgid(), os.Geteuid(), os.Getenv)
	}
)

func setReportOwner(file *os.File, owner fileOwner) error {
	if geteuid() != 0 {
		return nil
	}
	// Ownership is assigned before publication. If it fails, Write removes
	// the temporary file and leaves no unreadable final report behind.
	if err := file.Chown(owner.uid, owner.gid); err != nil {
		return fmt.Errorf("set report owner: %w", err)
	}
	return nil
}
