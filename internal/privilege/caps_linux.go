package privilege

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// capabilities lists the permitted or effective capability sets that are
// not empty, as /proc/self/status shows them.
func capabilities() (string, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return "", fmt.Errorf("read capabilities: %w", err)
	}
	defer f.Close()
	var held []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, value, ok := strings.Cut(sc.Text(), ":")
		if !ok || (name != "CapPrm" && name != "CapEff") {
			continue
		}
		if value = strings.TrimSpace(value); strings.Trim(value, "0") != "" {
			held = append(held, name+" "+value)
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read capabilities: %w", err)
	}
	return strings.Join(held, ", "), nil
}
