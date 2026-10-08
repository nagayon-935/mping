package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
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
}

type handlerConfig struct {
	streamInterval    time.Duration
	heartbeatInterval time.Duration
	generation        func() uint64
}

// Server is a running web UI listener.
type Server struct {
	srv       *http.Server
	addr      string
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// Start listens on 127.0.0.1 only — the UI is meant for a browser on the
// machine running mping, and mping may hold raw-socket privileges — and
// serves in the background until Close.
func Start(opts Options) (*Server, error) {
	if opts.Port < 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("web: port %d out of range 0-65535", opts.Port)
	}
	if opts.Source == nil {
		return nil, errors.New("web: nil Source")
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.Port)))
	if err != nil {
		return nil, fmt.Errorf("web: listen on 127.0.0.1:%d: %w", opts.Port, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		srv: &http.Server{
			Handler: newHandler(ctx, opts.Source, handlerConfig{
				streamInterval:    defaultStreamInterval,
				heartbeatInterval: defaultHeartbeatInterval,
			}),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
			// No WriteTimeout: /api/v1/stream responses are open-ended.
		},
		addr:   ln.Addr().String(),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		// Serve only ever returns ErrServerClosed after Shutdown, or a
		// listener error that leaves nothing to recover; Close reports
		// the shutdown outcome either way.
		_ = s.srv.Serve(ln)
	}()
	return s, nil
}

// Addr is the bound host:port.
func (s *Server) Addr() string { return s.addr }

// URL is the address to open in a browser.
func (s *Server) URL() string { return "http://" + s.addr + "/" }

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

// newHandler builds the full route table; ctx bounds open streams.
func newHandler(ctx context.Context, src *Source, cfg handlerConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/snapshot", handleSnapshot(src))
	mux.HandleFunc("GET /api/v1/stream", handleStream(ctx, src, cfg))
	mux.HandleFunc("GET /api/v1/targets/{id}/history", handleHistory(src))
	mux.HandleFunc("GET /api/v1/targets/{id}/events", handleEvents(src))
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(fmt.Sprintf("web: embedded static dir missing: %v", err))
	}
	mux.Handle("GET /", staticHandler(static))
	return guard(mux)
}

func staticHandler(fsys fs.FS) http.Handler {
	return http.FileServerFS(fsys)
}

// guard rejects requests that did not come from a same-machine page:
//   - a non-loopback Host header means DNS rebinding (an attacker's
//     hostname resolved to 127.0.0.1), so the browser would treat the
//     response as same-origin with the attacker's page;
//   - a non-loopback Origin header means another site's script is calling us.
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
		if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin) {
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

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return isLoopbackHost(u.Hostname())
}
