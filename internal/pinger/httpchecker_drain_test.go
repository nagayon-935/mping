package pinger

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// countingReader yields `remaining` bytes of nothing in particular and
// records how many a consumer actually pulled, so a test can assert on the
// read volume rather than on the (discarded) content.
type countingReader struct {
	remaining int
	read      int
	sawEOF    bool
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		r.sawEOF = true
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	r.remaining -= n
	r.read += n
	return n, nil
}

// TestDrainBody_StopsAtMaxDrainBytes is the guard for the unbounded-drain
// problem: the response body is read only so the connection can be reused,
// so a health check must never pull a large body end to end just to throw it
// away. client.Timeout bounds the time this takes but not the bytes.
func TestDrainBody_StopsAtMaxDrainBytes(t *testing.T) {
	body := &countingReader{remaining: maxDrainBytes * 4}

	drainBody(body)

	// maxDrainBytes+1 is the probe read that tells a capped body apart from
	// one that ended exactly at the cap; anything beyond that is the runaway
	// read this guard exists to catch.
	if body.read > maxDrainBytes+1 {
		t.Errorf("drained %d bytes, want at most maxDrainBytes+1 (%d)", body.read, maxDrainBytes+1)
	}
}

// TestDrainBody_ReadsExactlyMaxDrainBytesToEOF is the boundary the cap gets
// wrong when the limit is maxDrainBytes rather than maxDrainBytes+1:
// io.CopyN stops at its limit without reading past it, so a body of exactly
// maxDrainBytes would be fully consumed yet never report EOF, and net/http
// would drop the connection as if the body were oversized.
func TestDrainBody_ReadsExactlyMaxDrainBytesToEOF(t *testing.T) {
	body := &countingReader{remaining: maxDrainBytes}

	drainBody(body)

	if body.read != maxDrainBytes {
		t.Errorf("drained %d bytes, want the whole %d-byte body", body.read, maxDrainBytes)
	}
	if body.remaining != 0 {
		t.Errorf("body has %d bytes left, want it drained to EOF", body.remaining)
	}
	if !body.sawEOF {
		t.Error("drainBody stopped at the cap without reading to EOF; the connection would be dropped")
	}
}

// TestDrainBody_ReadsShortBodyFully is the other half of the contract: the
// common case must still be drained completely, otherwise the connection
// can't go back into the keep-alive pool.
func TestDrainBody_ReadsShortBodyFully(t *testing.T) {
	const size = 128
	body := &countingReader{remaining: size}

	drainBody(body)

	if body.read != size {
		t.Errorf("drained %d bytes, want the whole %d-byte body", body.read, size)
	}
}

// TestCheck_DrainsBoundedBody is the integration half of the guard. The unit
// tests above call drainBody directly, so putting an unbounded io.Copy back
// into check() would leave them green — the regression they exist to prevent
// lives in check(), not in the helper. Driving a real request through
// hc.check and asking the handler how much of an oversized body it managed
// to hand over closes that gap, and exercises the deferred Body.Close too.
func TestCheck_DrainsBoundedBody(t *testing.T) {
	// Far beyond any plausible socket buffer, so a bounded drain has to cut
	// the transfer short. Only a failing run actually writes this much.
	const bodySize = 16 << 20

	wrote := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := make([]byte, 64<<10)
		written := 0
		for written < bodySize {
			n, err := w.Write(chunk)
			written += n
			if err != nil {
				break
			}
		}
		wrote <- written
	}))
	defer srv.Close()

	hc := NewHTTPChecker([]string{srv.URL}, time.Second, 10*time.Second, BindConfig{})
	r := hc.Results()[0]

	hc.check(r)

	select {
	case written := <-wrote:
		if written >= bodySize {
			t.Errorf("handler wrote all %d bytes; check() drained the body unbounded", bodySize)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("handler never finished: check() is still reading the body")
	}

	if v := r.GetView(); v.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", v.StatusCode, http.StatusOK)
	}
}
