package pinger

import (
	"io"
	"testing"
)

// countingReader yields `remaining` bytes of nothing in particular and
// records how many a consumer actually pulled, so a test can assert on the
// read volume rather than on the (discarded) content.
type countingReader struct {
	remaining int
	read      int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
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

	if body.read > maxDrainBytes {
		t.Errorf("drained %d bytes, want at most maxDrainBytes (%d)", body.read, maxDrainBytes)
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
