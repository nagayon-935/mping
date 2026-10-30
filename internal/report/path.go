package report

import (
	"fmt"
	"path/filepath"
	"strings"
)

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
