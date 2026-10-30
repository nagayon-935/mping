package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/web"
)

// TestMain lets the test binary stand in for mping as the web UI process:
// spawnWebProcess runs os.Executable(), which here is this binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == webProcessCommand {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// staticProvider is a read-only web.Provider over fixed targets.
type staticProvider struct{ targets []*stats.TargetStats }

func (p staticProvider) Targets() []*stats.TargetStats         { return p.targets }
func (p staticProvider) HTTPResults() []*stats.HTTPCheckResult { return nil }
func (p staticProvider) Meta() web.Meta                        { return web.Meta{IntervalMs: 1000} }

func TestWebUIRunsItsFrontendInAnUnprivilegedProcess(t *testing.T) {
	src := web.NewSource()
	src.Set(staticProvider{targets: []*stats.TargetStats{stats.NewTargetStats("router.example")}})
	srv, err := startSplitWebUI(web.Options{Port: 0, Source: src})
	if err != nil {
		t.Fatalf("startSplitWebUI: %v", err)
	}
	ui := srv.(*webUI)
	proc := ui.frontend.(*webProcess)
	closed := false
	t.Cleanup(func() {
		if !closed {
			ui.Close()
		}
	})

	resp, err := http.Get(srv.URL() + "api/v1/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "router.example") {
		t.Fatalf("snapshot through the web process = %d %s", resp.StatusCode, body)
	}
	// The token check happens in mping, behind the socket.
	_, token, _ := strings.Cut(srv.ControlURL(), "#token=")
	for header, want := range map[string]int{"": http.StatusForbidden, token: http.StatusNotImplemented} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL()+"api/v1/reset", nil)
		if header != "" {
			req.Header.Set("X-Mping-Token", header)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("reset with token %v = %d, want %d", header != "", resp.StatusCode, want)
		}
	}

	if runtime.GOOS == "linux" {
		status := procStatus(t, proc.cmd.Process.Pid)
		want := webProcessIdentity()
		if uid := strings.Fields(status["Uid"]); len(uid) == 0 || uid[0] != strconv.Itoa(want.uid) || uid[1] != uid[0] {
			t.Errorf("web process Uid = %q, want %d throughout", status["Uid"], want.uid)
		}
		for _, set := range []string{"CapPrm", "CapEff"} {
			if strings.Trim(status[set], "0") != "" {
				t.Errorf("web process %s = %s, want none", set, status[set])
			}
		}
		if status["NoNewPrivs"] != "1" {
			t.Errorf("web process NoNewPrivs = %q, want 1", status["NoNewPrivs"])
		}
	}

	closed = true
	if err := ui.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-proc.done:
	default:
		t.Fatal("the web process is still running after Close")
	}
	if _, err := os.Stat(ui.dir); !os.IsNotExist(err) {
		t.Errorf("socket directory %s left behind", ui.dir)
	}
}

func TestWebUIReportsAWebProcessThatCannotListen(t *testing.T) {
	taken, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := taken.Addr().(*net.TCPAddr).Port

	_, err = startSplitWebUI(web.Options{Port: port, Source: web.NewSource()})
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("startSplitWebUI on a taken port = %v, want the listen error", err)
	}
}

func TestWebProcessEnvDropsTheControlToken(t *testing.T) {
	got := webProcessEnv([]string{"HOME=/home/u", "MPING_WEB_TOKEN=secret-secret-secret", "TMPDIR=/tmp"})
	if strings.Contains(strings.Join(got, " "), "MPING_WEB_TOKEN") || len(got) != 2 {
		t.Fatalf("env = %q", got)
	}
}

func TestWebProcessIdentityNeverIsRoot(t *testing.T) {
	if os.Getuid() != 0 {
		if id := webProcessIdentity(); id.uid != os.Getuid() || id.gid != os.Getgid() {
			t.Fatalf("identity = %+v, want the invoking user", id)
		}
		return
	}
	t.Setenv("SUDO_UID", "1234")
	t.Setenv("SUDO_GID", "5678")
	if id := webProcessIdentity(); id.uid != 1234 || id.gid != 5678 {
		t.Fatalf("under sudo: identity = %+v, want the sudo caller", id)
	}
	t.Setenv("SUDO_UID", "")
	if id := webProcessIdentity(); id.uid == 0 || id.keep {
		t.Fatalf("as root: identity = %+v, want nobody", id)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var b strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		b.WriteString(sc.Text())
	}
	return b.String()
}

// procStatus reads /proc/<pid>/status into name -> value.
func procStatus(t *testing.T, pid int) map[string]string {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok {
			out[name] = strings.TrimSpace(value)
		}
	}
	return out
}

// TestWebProcessRemovesItsSocketDirectoryOnExit covers a killed mping: the
// web process sees its stdin close and cleans up the socket directory that
// mping no longer can.
func TestWebProcessRemovesItsSocketDirectoryOnExit(t *testing.T) {
	id := webProcessIdentity()
	dir, socket, ln, err := listenWebSocket(id)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	front, err := spawnWebProcess(socket, 0, id)
	if err != nil {
		t.Fatalf("spawnWebProcess: %v", err)
	}

	if err := front.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		os.RemoveAll(dir)
		t.Fatalf("socket directory %s left behind (stat err = %v)", dir, err)
	}
}
