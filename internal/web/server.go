package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static
var staticFiles embed.FS

const (
	defaultStreamInterval    = time.Second
	defaultHeartbeatInterval = 15 * time.Second
	shutdownTimeout          = 2 * time.Second
)

// Options configures Start.
type Options struct {
	// Port is the TCP port on 127.0.0.1; 0 lets the kernel pick one.
	Port   int
	Source *Source
	// Token fixes the control token (e.g. from MPING_WEB_TOKEN for an
	// unattended run). Empty generates a random one per launch.
	Token string
}

type handlerConfig struct {
	streamInterval    time.Duration
	heartbeatInterval time.Duration
	generation        func() uint64
	// token authorizes control requests; empty makes the server read-only.
	token string
}

// Server is a running web UI listener.
type Server struct {
	srv       *http.Server
	addr      string
	token     string
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// Start listens on loopback only — the UI is meant for a browser on the
// machine running mping, and mping may hold raw-socket privileges — and
// serves in the background until Close. 127.0.0.1 is required; [::1] on the
// same port is added when available so a browser that resolves "localhost"
// to IPv6 first still connects directly.
func Start(opts Options) (*Server, error) {
	token := opts.Token
	if token == "" {
		var err error
		if token, err = NewToken(); err != nil {
			return nil, fmt.Errorf("web: %w", err)
		}
	} else if err := CheckToken(token); err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	return start(opts, token)
}

// start is Start with a caller-chosen token (TestDevServer pins one so
// browser checks can authenticate).
func start(opts Options, token string) (*Server, error) {
	if opts.Source == nil {
		return nil, errors.New("web: nil Source")
	}
	return serve(opts.Port, token, func(ctx context.Context, cfg handlerConfig) http.Handler {
		return newHandler(ctx, opts.Source, cfg)
	})
}

// FrontendOptions configures StartFrontend.
type FrontendOptions struct {
	// Port is the TCP port on 127.0.0.1; 0 lets the kernel pick one.
	Port int
	// Backend is the UNIX socket a ServeBackend in another process serves.
	Backend string
}

// StartFrontend serves the browser-facing half of the web UI on loopback,
// relaying the API to the backend on opts.Backend. It knows no control
// token: the backend checks it, and the caller that chose it builds the
// control URL.
func StartFrontend(opts FrontendOptions) (*Server, error) {
	if opts.Backend == "" {
		return nil, errors.New("web: no backend socket")
	}
	backend := unixTransport(opts.Backend)
	return serve(opts.Port, "", func(ctx context.Context, cfg handlerConfig) http.Handler {
		return newFrontendHandler(ctx, cfg, backend)
	})
}

// serve listens on loopback and serves the handler built for the server's
// lifetime context.
func serve(port int, token string, handler func(context.Context, handlerConfig) http.Handler) (*Server, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("web: port %d out of range 0-65535", port)
	}
	ln4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("web: listen on 127.0.0.1:%d: %w", port, err)
	}
	listeners := []net.Listener{ln4}
	port = ln4.Addr().(*net.TCPAddr).Port
	// Best effort by design: IPv6 may be disabled, and the advertised URL is
	// the 127.0.0.1 one, so a missing [::1] listener loses nothing.
	if ln6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(port))); err == nil {
		listeners = append(listeners, ln6)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		srv: &http.Server{
			Handler: handler(ctx, handlerConfig{
				streamInterval:    defaultStreamInterval,
				heartbeatInterval: defaultHeartbeatInterval,
				token:             token,
			}),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
			// No WriteTimeout: /api/v1/stream responses are open-ended.
		},
		addr:   ln4.Addr().String(),
		token:  token,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	var wg sync.WaitGroup
	for _, ln := range listeners {
		wg.Go(func() {
			// Serve only ever returns ErrServerClosed after Shutdown, or a
			// listener error that leaves nothing to recover; Close reports
			// the shutdown outcome either way.
			_ = s.srv.Serve(ln)
		})
	}
	go func() {
		wg.Wait()
		close(s.done)
	}()
	return s, nil
}

// URL is the read-only address to open in a browser.
func (s *Server) URL() string {
	_, port, _ := net.SplitHostPort(s.addr)
	return "http://localhost:" + port + "/"
}

// ControlURL is URL plus the per-launch control token. The token rides in
// the fragment, which browsers never send to the server or log in requests;
// the page moves it into storage and sends it as a header.
func (s *Server) ControlURL() string { return s.URL() + "#token=" + s.token }

// Close ends open streams, shuts the listener down, and waits for the serve
// goroutine to exit. Safe to call more than once.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		// Streams never go idle on their own, so end them before Shutdown
		// waits for in-flight requests.
		s.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := s.srv.Shutdown(ctx); err != nil {
			s.closeErr = fmt.Errorf("web: shutdown: %w", err)
			_ = s.srv.Close()
		}
		<-s.done
	})
	return s.closeErr
}

// newHandler is the whole web UI in one process: the frontend reaches the
// backend through an in-process transport instead of a socket. Start and
// the tests use it; mping itself runs the two halves in separate processes
// (StartFrontend, ServeBackend). ctx bounds open streams.
func newHandler(ctx context.Context, src *Source, cfg handlerConfig) http.Handler {
	return newFrontendHandler(ctx, cfg, handlerTransport{newBackendHandler(src, cfg.token, cfg.generation)})
}

// staticHandler serves files from fsys, answering 404 for any directory
// other than the root so http.FileServer never renders a listing.
//
// Embedded files have no modification time, so browsers would otherwise
// cache them heuristically and could run an older dashboard against a newer
// API after an upgrade. Every response must be revalidated; a content hash
// ETag keeps that revalidation a cheap 304.
func staticHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	etags := contentETags(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if fi, err := fs.Stat(fsys, name); err != nil || fi.IsDir() {
				http.NotFound(w, r)
				return
			}
		}
		if name == "" {
			name = "index.html"
		}
		w.Header().Set("Cache-Control", "no-cache")
		if tag, ok := etags[name]; ok {
			w.Header().Set("ETag", tag)
		}
		files.ServeHTTP(w, r)
	})
}

// contentETags hashes every regular file in fsys. A file that cannot be read
// is left without an ETag and is simply re-sent on each request.
func contentETags(fsys fs.FS) map[string]string {
	tags := make(map[string]string)
	_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		tags[name] = `"` + hex.EncodeToString(sum[:16]) + `"`
		return nil
	})
	return tags
}

// guard rejects requests that did not come from a same-machine page:
//   - a non-loopback Host header means DNS rebinding (an attacker's
//     hostname resolved to 127.0.0.1), so the browser would treat the
//     response as same-origin with the attacker's page;
//   - an Origin other than exactly this server (scheme, host and port) means
//     another page — a foreign site or another local app on a different
//     port — is calling us.
//
// It also sets defensive response headers on everything.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")

		if !isLoopbackHost(hostOnly(r.Host)) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !isSameOrigin(origin, r.Host) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostOnly strips an optional port (and IPv6 brackets) from a Host value.
func hostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isSameOrigin reports whether origin is http://<host> for the Host header
// the browser sent, which guard has already confirmed is loopback.
func isSameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Path != "" || u.RawQuery != "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}
