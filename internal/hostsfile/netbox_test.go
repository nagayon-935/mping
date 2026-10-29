package hostsfile

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNetBoxCSV_StatusFilteringAndVariants(t *testing.T) {
	content := "\xef\xbb\xbf# NetBox export\n" +
		"Address,Status,DNS_Name,Device,Virtual Machine,Interface\n" +
		"10.0.0.1/24,Active,web01,srv01,,eth0\n" +
		"10.0.0.2/24,Deprecated,old01,srv02,,eth0\n" +
		"10.0.0.3/24,Reserved,res01,,,\n" +
		"10.0.0.4/24,DHCP,dhcp01,,,\n" +
		"10.0.0.5/24,active,,srv05,,eth0\n" +
		"2001:db8::1/64,,, ,vm01,ens3\n" +
		"10.0.0.6,,,,,\n"

	dir := t.TempDir()
	path := writeTestFile(t, dir, "export.txt", content)

	doc, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Entry{
		{Host: "10.0.0.1", Name: "web01"},
		{Host: "10.0.0.5", Name: "srv05"},
		{Host: "2001:db8::1", Name: "vm01"},
		{Host: "10.0.0.6"},
	}
	if !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("got %#v, want %#v", doc.Hosts, want)
	}
}

func TestNetBoxCSV_Errors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "empty IP address column",
			content: "IP Address,Status\n,Active\n",
			wantErr: "line 2: IP Address column is empty",
		},
		{
			name:    "invalid CIDR in IP address column",
			content: "IP Address,Status\nnot-an-ip/24,Active\n",
			wantErr: "line 2: invalid IP address",
		},
		{
			name:    "invalid bare IP in IP address column",
			content: "IP Address,Status\ns3cret-hostname,Active\n",
			wantErr: "line 2: invalid IP address",
		},
		{
			name:    "malformed CSV quote",
			content: "IP Address,Status,Comments\n10.0.0.1/24,Active,\"unclosed\n",
			wantErr: "line 2:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestFile(t, t.TempDir(), "bad.csv", tt.content)
			_, err := readIncludeFile(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("error %q echoes file content", err)
			}
		})
	}
}

func TestNetBoxCSV_StopsAtTotalLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("IP Address,Status\n")
	for i := range maxHostsFileTargets + 2 {
		fmt.Fprintf(&b, "10.0.%d.%d/24,Active\n", i/256, i%256)
	}
	path := writeTestFile(t, t.TempDir(), "huge.csv", b.String())
	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), "more than 4096 targets") {
		t.Fatalf("err = %v, want total-limit error", err)
	}
}
