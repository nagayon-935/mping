package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode"

	"github.com/nagayon-935/mping/internal/report"
)

// Controller is implemented by providers that let the browser change the
// running measurement. Errors are shown to the user verbatim.
type Controller interface {
	AddHost(host string) error
	DeleteTarget(id uint64) error
	ResetStats()
}

type MeasurementController interface {
	StopMeasurements() error
	RestartMeasurements() error
}
type ReportController interface{ CaptureReport() (report.Report, error) }

func handleMeasurement(restart bool) func(http.ResponseWriter, *http.Request, Controller) {
	return func(w http.ResponseWriter, r *http.Request, c Controller) {
		m, ok := c.(MeasurementController)
		if !ok {
			writeError(w, http.StatusNotImplemented, "measurement controls unavailable")
			return
		}
		var err error
		if restart {
			err = m.RestartMeasurements()
		} else {
			err = m.StopMeasurements()
		}
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleReport(w http.ResponseWriter, r *http.Request, c Controller) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		writeError(w, http.StatusBadRequest, "format must be json or csv")
		return
	}
	p, ok := c.(ReportController)
	if !ok {
		writeError(w, http.StatusNotImplemented, "reports unavailable")
		return
	}
	snapshot, err := p.CaptureReport()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if format == "csv" {
		data, err := snapshot.CSV()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="mping-report.csv"`)
		_, _ = w.Write(data)
	} else {
		w.Header().Set("Content-Disposition", `attachment; filename="mping-report.json"`)
		writeJSON(w, snapshot)
	}
}

const (
	// tokenHeader carries the per-launch control token. A custom header
	// (rather than a cookie) cannot be attached by another site's page
	// without a CORS preflight this server never approves, and it is not
	// shared with other services on 127.0.0.1 the way a cookie would be.
	tokenHeader    = "X-Mping-Token"
	maxControlBody = 4 << 10
	maxHostLen     = 253
)

// SessionResponse is the body of /api/v1/session.
type SessionResponse struct {
	Control bool `json:"control"`
}

// minTokenLen bounds a configured token from below; generated ones are 64
// hex characters.
const minTokenLen = 16

// CheckToken accepts tokens that are long enough and URL-fragment safe
// (unreserved characters only). Errors never repeat the token.
func CheckToken(token string) error {
	if len(token) < minTokenLen {
		return fmt.Errorf("control token must be at least %d characters", minTokenLen)
	}
	for _, r := range token {
		ok := r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-._~", r))
		if !ok {
			return errors.New("control token may only contain letters, digits and -._~")
		}
	}
	return nil
}

// NewToken returns a random control token.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate control token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// hasToken reports whether r carries the control token. An unset token
// never matches, so a server built without one is read-only.
func hasToken(r *http.Request, token string) bool {
	got := r.Header.Get(tokenHeader)
	return token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func handleSession(src *Source, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _, _ := src.load()
		_, canControl := p.(Controller)
		writeJSON(w, SessionResponse{Control: canControl && hasToken(r, token)})
	}
}

// controlled wraps a mutating handler: token first, then a live controller.
// Origin and Host were already checked by guard.
func controlled(src *Source, token string, next func(http.ResponseWriter, *http.Request, Controller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hasToken(r, token) {
			writeError(w, http.StatusForbidden, "control token missing or invalid; press b in mping's TUI to show or copy the control URL")
			return
		}
		p, state, _ := src.load()
		c, ok := p.(Controller)
		if p != nil && !ok {
			writeError(w, http.StatusNotImplemented, "this mping session does not accept changes")
			return
		}
		if state != StateRunning {
			writeError(w, http.StatusConflict, fmt.Sprintf("mping is %s; try again shortly", state))
			return
		}
		next(w, r, c)
	}
}

type addHostRequest struct {
	Host string `json:"host"`
}

func handleAddHost(w http.ResponseWriter, r *http.Request, c Controller) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "body must be application/json")
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBody))
	dec.DisallowUnknownFields()
	var body addHostRequest
	if err := dec.Decode(&body); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "body must be {\"host\": \"...\"}")
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "body must contain a single JSON object")
		return
	}
	host, err := validHost(body.Host)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := c.AddHost(host); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	body.Host = host
	_ = json.NewEncoder(w).Encode(body)
}

// validHost does the checks the supervisor cannot express as a user-facing
// error: shape only. Duplicates and resolvability stay the supervisor's call.
func validHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	switch {
	case host == "":
		return "", errors.New("host cannot be empty")
	case len(host) > maxHostLen:
		return "", fmt.Errorf("host is longer than %d characters", maxHostLen)
	// U+FEFF is included because the browser's \s treats it as space;
	// both sides must refuse the same hosts.
	case strings.IndexFunc(host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\uFEFF' }) >= 0:
		return "", errors.New("host must not contain spaces or control characters")
	}
	return host, nil
}

func handleDeleteTarget(w http.ResponseWriter, r *http.Request, c Controller) {
	id, ok := targetID(w, r)
	if !ok {
		return
	}
	if err := c.DeleteTarget(id); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleReset(w http.ResponseWriter, r *http.Request, c Controller) {
	c.ResetStats()
	w.WriteHeader(http.StatusNoContent)
}
