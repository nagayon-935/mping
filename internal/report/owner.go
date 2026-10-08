package report

import (
	"fmt"
	"os"
	"strconv"
)

type fileOwner struct{ uid, gid int }

// For setuid installations, the real IDs identify the caller. sudo sets
// both real and effective UID to root, so use its original caller IDs only
// in that case. Ordinary users' environment variables are never consulted.
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

func setReportOwner(file *os.File, owner fileOwner) error {
	if os.Geteuid() != 0 {
		return nil
	}
	// Ownership is assigned before publication. If it fails, Write removes
	// the temporary file and leaves no unreadable final report behind.
	if err := file.Chown(owner.uid, owner.gid); err != nil {
		return fmt.Errorf("set report owner: %w", err)
	}
	return nil
}
