package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/web"
)

// webServer is the running web UI as run() uses it: *webUI in mping, a
// *web.Server in tests that run the whole UI in-process.
type webServer interface {
	URL() string
	ControlURL() string
	Close() error
}

// webUI is the web UI split across two processes: the backend (the API over
// the measurements) in mping, on a UNIX socket in a private directory, and
// the browser-facing frontend in an unprivileged web process.
type webUI struct {
	backend  *web.Backend
	dir      string
	frontend webFrontend
	token    string
}

// webFrontend is the browser-facing half, reached on its URL.
type webFrontend interface {
	URL() string
	Close() error
}

func (w *webUI) URL() string        { return w.frontend.URL() }
func (w *webUI) ControlURL() string { return w.URL() + "#token=" + w.token }

// Close stops the frontend first, so its open streams can still fetch the
// final state from the backend, then the backend.
func (w *webUI) Close() error {
	err := errors.Join(w.frontend.Close(), w.backend.Close())
	_ = os.RemoveAll(w.dir)
	return err
}

// startWebFrontend starts the frontend in front of the backend socket; a
// seam so tests can run it in-process.
var startWebFrontend = spawnWebProcess

// startSplitWebUI serves opts.Source's API on a socket only the web process
// can open and starts that process on opts.Port.
func startSplitWebUI(opts web.Options) (webServer, error) {
	token := opts.Token
	if token == "" {
		var err error
		if token, err = web.NewToken(); err != nil {
			return nil, err
		}
	}
	id := webProcessIdentity()
	dir, socket, ln, err := listenWebSocket(id)
	if err != nil {
		return nil, err
	}
	backend, err := web.ServeBackend(ln, opts.Source, token)
	if err != nil {
		ln.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	frontend, err := startWebFrontend(socket, opts.Port, id)
	if err != nil {
		backend.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	return &webUI{backend: backend, dir: dir, frontend: frontend, token: token}, nil
}

// identity is who the web process runs as; keep leaves it as started (no
// user IDs on this platform).
type identity struct {
	uid, gid int
	keep     bool
}

// webProcessIdentity is the user who started mping (the caller under sudo),
// or nobody when that is root itself: the web process never runs as root.
func webProcessIdentity() identity {
	uid, gid := os.Getuid(), os.Getgid()
	if uid < 0 {
		return identity{keep: true}
	}
	if uid == 0 {
		if su, sg := os.Getenv("SUDO_UID"), os.Getenv("SUDO_GID"); su != "" && sg != "" {
			if u, err := strconv.ParseUint(su, 10, 32); err == nil && u != 0 {
				if g, err := strconv.ParseUint(sg, 10, 32); err == nil {
					return identity{uid: int(u), gid: int(g)}
				}
			}
		}
		if nobody, err := user.Lookup("nobody"); err == nil {
			u, uerr := strconv.ParseInt(nobody.Uid, 10, 64)
			g, gerr := strconv.ParseInt(nobody.Gid, 10, 64)
			if uerr == nil && gerr == nil {
				// macOS lists nobody as -2: the 32-bit ID 4294967294.
				return identity{uid: int(uint32(u)), gid: int(uint32(g))}
			}
		}
		return identity{uid: 65534, gid: 65534}
	}
	return identity{uid: uid, gid: gid}
}

// listenWebSocket creates a private directory, readable only by id, holding
// the backend's socket. Only id (and root) can connect: the backend checks
// neither Host nor Origin, so nothing else may reach it.
func listenWebSocket(id identity) (dir, socket string, ln net.Listener, err error) {
	base := os.TempDir()
	// A socket path longer than sun_path (104 bytes on macOS) cannot bind.
	if len(base) > 60 && runtime.GOOS != "windows" {
		base = "/tmp"
	}
	if dir, err = os.MkdirTemp(base, "mping-web-"); err != nil {
		return "", "", nil, fmt.Errorf("web: %w", err)
	}
	socket = filepath.Join(dir, "api.sock")
	if ln, err = net.Listen("unix", socket); err != nil {
		os.RemoveAll(dir)
		return "", "", nil, fmt.Errorf("web: %w", err)
	}
	if !id.keep && os.Geteuid() == 0 {
		for _, path := range []string{dir, socket} {
			if err = os.Chown(path, id.uid, id.gid); err != nil {
				ln.Close()
				os.RemoveAll(dir)
				return "", "", nil, fmt.Errorf("web: %w", err)
			}
		}
	}
	return dir, socket, ln, nil
}

// webProcessStartTimeout bounds how long the web process may take to report
// that it is serving.
const webProcessStartTimeout = 10 * time.Second

// webProcess is the frontend running as `mping __web`. Its stdin stays open
// for as long as mping wants it: closing it, or mping exiting, ends it.
type webProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	url    string
	stderr *limitedBuffer
	done   chan struct{}
}

func (p *webProcess) URL() string { return p.url }

// Close asks the process to stop and waits for it, killing it if it does
// not exit in time.
func (p *webProcess) Close() error {
	p.stdin.Close()
	select {
	case <-p.done:
		return nil
	case <-time.After(2 * shutdownGrace):
		_ = p.cmd.Process.Kill()
		<-p.done
		return errors.New("web: the web UI process did not exit in time")
	}
}

// shutdownGrace is how long the web process gets to end its streams.
const shutdownGrace = 3 * time.Second

// spawnWebProcess runs the frontend as `mping __web`, which drops
// privileges to id before serving, and waits until it reports its URL.
func spawnWebProcess(socket string, port int, id identity) (webFrontend, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	args := []string{webProcessCommand, "--socket", socket, "--port", strconv.Itoa(port)}
	if !id.keep {
		args = append(args, "--uid", strconv.Itoa(id.uid), "--gid", strconv.Itoa(id.gid))
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = webProcessEnv(os.Environ())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	p := &webProcess{cmd: cmd, stdin: stdin, stderr: &limitedBuffer{max: 4 << 10}, done: make(chan struct{})}
	cmd.Stderr = p.stderr
	if err := startUnprivileged(cmd); err != nil {
		return nil, fmt.Errorf("web: start the web UI process: %w", err)
	}

	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lines <- strings.TrimSpace(line)
		_, _ = io.Copy(io.Discard, stdout)
	}()
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()

	fail := func(err error) (webFrontend, error) {
		stdin.Close()
		_ = cmd.Process.Kill()
		<-p.done
		if detail := strings.TrimSpace(p.stderr.String()); detail != "" {
			err = fmt.Errorf("%w (%s)", err, detail)
		}
		return nil, err
	}
	select {
	case line := <-lines:
		if url, ok := strings.CutPrefix(line, "ready "); ok {
			p.url = url
			return p, nil
		}
		if msg, ok := strings.CutPrefix(line, "error "); ok {
			return fail(errors.New(msg))
		}
		return fail(errors.New("web: the web UI process exited before serving"))
	case <-time.After(webProcessStartTimeout):
		return fail(errors.New("web: the web UI process did not start in time"))
	}
}

// webProcessEnv is the environment for the web process, without the
// control token: the frontend never needs it.
func webProcessEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "MPING_WEB_TOKEN=") {
			out = append(out, kv)
		}
	}
	return out
}

// limitedBuffer keeps the first max bytes written to it, for error reports.
type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
