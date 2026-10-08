package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestReadIncludeFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []hostEntry
	}{
		{
			name:    "one host per line with comments and blanks",
			content: "# core routers\n10.0.0.1\n\n  gw.example.com   # inline comment\n",
			want:    []hostEntry{{Host: "10.0.0.1"}, {Host: "gw.example.com"}},
		},
		{
			name:    "CSV columns host,name,dscp",
			content: "10.0.0.1,core-sw01\n10.0.0.2, core-sw02 , EF\n10.0.0.3,,CS1\n",
			want: []hostEntry{
				{Host: "10.0.0.1", Name: "core-sw01"},
				{Host: "10.0.0.2", Name: "core-sw02", DSCP: "EF"},
				{Host: "10.0.0.3", DSCP: "CS1"},
			},
		},
		{
			name:    "header row is skipped",
			content: "Host,Name,DSCP\n10.0.0.1,core\n",
			want:    []hostEntry{{Host: "10.0.0.1", Name: "core"}},
		},
		{
			name:    "hostname header row is skipped",
			content: "HOSTNAME,name\n10.0.0.1,core\n",
			want:    []hostEntry{{Host: "10.0.0.1", Name: "core"}},
		},
		{
			name:    "IPv6 with zone and underscores in hostnames are valid",
			content: "fe80::1%en0\n_srv.example.com\n",
			want:    []hostEntry{{Host: "fe80::1%en0"}, {Host: "_srv.example.com"}},
		},
		{
			name:    "patterns are expanded",
			content: "10.0.0.1-2\nsw{1..2}.lab,,EF\n",
			want: []hostEntry{
				{Host: "10.0.0.1"}, {Host: "10.0.0.2"},
				{Host: "sw1.lab", DSCP: "EF"}, {Host: "sw2.lab", DSCP: "EF"},
			},
		},
		{
			name:    "CRLF line endings",
			content: "10.0.0.1,a\r\n10.0.0.2,b\r\n",
			want:    []hostEntry{{Host: "10.0.0.1", Name: "a"}, {Host: "10.0.0.2", Name: "b"}},
		},
		{
			name:    "empty file yields no hosts",
			content: "# nothing yet\n",
			want:    []hostEntry{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestFile(t, t.TempDir(), "list.csv", tt.content)

			got, err := readIncludeFile(path)

			if err != nil {
				t.Fatalf("readIncludeFile: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestReadIncludeFile_Errors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "too many columns", content: "10.0.0.1\n10.0.0.2,a,EF,extra\n", wantErr: "line 2: expected at most 3 columns"},
		{name: "empty host column", content: ",name-only\n", wantErr: "line 1: host column is empty"},
		{name: "invalid DSCP", content: "10.0.0.1,a,bogus\n", wantErr: "line 1: invalid dscp"},
		{name: "invalid pattern", content: "10.0.0.0/16\n", wantErr: "line 1: expands to more than"},
		{name: "name on multi-host pattern", content: "10.0.0.1-5,pair\n", wantErr: "line 1: name cannot be used"},
		{name: "host with a space", content: "10.0.0.1\nsw 01.lab\n", wantErr: "line 2: host is not a valid IP address or hostname"},
		{name: "host with shell characters", content: "a;b\n", wantErr: "line 1: host is not a valid IP address or hostname"},
		{name: "overlong hostname", content: strings.Repeat("a", 254) + "\n", wantErr: "line 1: host is not a valid IP address or hostname"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestFile(t, t.TempDir(), "list.csv", tt.content)

			_, err := readIncludeFile(path)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error %q should name the include file", err)
			}
		})
	}
}

func TestReadIncludeFile_ErrorsDoNotEchoContent(t *testing.T) {
	// mping may run setuid root (macOS install.sh); an include error must
	// not turn it into a way to print another user's file contents.
	tests := []struct{ name, content string }{
		{name: "passwd-style line", content: "root:s3cret:0:0:/var/root:/bin/sh\n"},
		{name: "config-style line", content: "Defaults s3cret_env\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestFile(t, t.TempDir(), "secret", tt.content)

			_, err := readIncludeFile(path)

			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("error %q echoes file content", err)
			}
		})
	}
}

func TestReadIncludeFile_RequiresRealUserReadAccess(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "list.txt", "10.0.0.1\n")
	orig := realUserCanRead
	t.Cleanup(func() { realUserCanRead = orig })
	realUserCanRead = func(string) error { return errors.New("permission denied") }

	_, err := readIncludeFile(path)

	if err == nil || !strings.Contains(err.Error(), "not readable by the invoking user") {
		t.Fatalf("error = %v, want a real-user access error", err)
	}
}

func TestReadIncludeFile_RejectsNonRegularFiles(t *testing.T) {
	_, err := readIncludeFile(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want 'not a regular file'", err)
	}
}

func TestReadIncludeFile_StopsAtTotalLimit(t *testing.T) {
	var b strings.Builder
	for range 5 {
		b.WriteString("10.0.0.0/22\n") // 1022 hosts each → 5110 total
	}
	path := writeTestFile(t, t.TempDir(), "big.txt", b.String())

	_, err := readIncludeFile(path)

	if err == nil || !strings.Contains(err.Error(), "more than 4096 targets") {
		t.Fatalf("error = %v, want total-limit error", err)
	}
}

func TestReadIncludeFile_MissingFile(t *testing.T) {
	_, err := readIncludeFile(filepath.Join(t.TempDir(), "missing.csv"))
	if err == nil || !strings.Contains(err.Error(), "missing.csv") {
		t.Fatalf("error = %v, want it to name the missing file", err)
	}
}
