package hostsfile

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadIncludeFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []Entry
	}{
		{
			name:    "NetBox export",
			content: netBoxCSV("10.0.0.1/24,Active,core-sw01", "10.0.0.2/24,Active,"),
			want:    []Entry{{Host: "10.0.0.1", Name: "core-sw01"}, {Host: "10.0.0.2"}},
		},
		{
			name:    "CRLF line endings",
			content: "IP Address,Status,DNS Name\r\n10.0.0.1/24,Active,a\r\n10.0.0.2/24,Active,b\r\n",
			want:    []Entry{{Host: "10.0.0.1", Name: "a"}, {Host: "10.0.0.2", Name: "b"}},
		},
		{
			name:    "a file with only comments yields no hosts",
			content: "# nothing yet\n",
			want:    []Entry{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestFile(t, t.TempDir(), "list.csv", tt.content)

			got, err := readIncludeFile(path)

			if err != nil {
				t.Fatalf("readIncludeFile: %v", err)
			}
			for i := range got {
				got[i].netBox = nil // name resolution state, checked via Parse
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
		{name: "plain host per line", content: "10.0.0.1\ngw.example.com\n", wantErr: "not a NetBox IP addresses CSV export"},
		{name: "plain host,name CSV", content: "host,name\n10.0.0.1,core\n", wantErr: "not a NetBox IP addresses CSV export"},
		{name: "invalid NetBox row", content: netBoxCSV("10.0.0.1/24,Active,a", "not-an-ip,Active,b"), wantErr: "line 3: invalid IP address"},
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

func TestReadIncludeFile_RejectsNonRegularFiles(t *testing.T) {
	_, err := readIncludeFile(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want 'not a regular file'", err)
	}
}

func TestReadIncludeFile_StopsAtTotalLimit(t *testing.T) {
	rows := make([]string, 4097)
	for i := range rows {
		rows[i] = fmt.Sprintf("10.0.%d.%d/16,Active,", i/256, i%256)
	}
	path := writeTestFile(t, t.TempDir(), "big.csv", netBoxCSV(rows...))

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

func TestParse_IncludeTildePathIsRelativeToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTestFile(t, home, "list.csv", netBoxCSV("192.0.2.1/24,Active,", "192.0.2.2/24,Active,edge"))
	path := writeTestFile(t, t.TempDir(), "hosts.yaml", "include: ~/list.csv\n")

	doc, err := Parse(path)

	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []Entry{{Host: "192.0.2.1"}, {Host: "192.0.2.2", Name: "edge"}}; !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("hosts = %+v, want %+v", doc.Hosts, want)
	}
}
