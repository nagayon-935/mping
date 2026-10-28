package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/nagayon-935/mping/internal/stats"
)

var errNoProvider = errors.New("statistics are not available yet")

// snapshotKey identifies one state of the world: a stats generation plus a
// Source version (provider swap or reload flag change).
type snapshotKey struct {
	gen, version uint64
}

// snapshotCache encodes one snapshot per snapshotKey and hands the same
// bytes to every caller, so N open streams cost one build per change rather
// than N. It is safe for concurrent use; the mutex also keeps concurrent
// callers from building the same snapshot twice.
type snapshotCache struct {
	src        *Source
	generation func() uint64

	mu   sync.Mutex
	ok   bool
	key  snapshotKey
	body []byte
}

func newSnapshotCache(src *Source, generation func() uint64) *snapshotCache {
	return &snapshotCache{src: src, generation: generation}
}

// get returns the encoded SnapshotResponse and the key it was built for.
func (c *snapshotCache) get() ([]byte, snapshotKey, error) {
	p, state, version := c.src.load()
	if p == nil {
		return nil, snapshotKey{}, errNoProvider
	}
	// Read the generation before building: a change racing with the build
	// then shows up as a new key on the next call instead of being lost.
	key := snapshotKey{gen: c.generation(), version: version}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && c.key == key {
		return c.body, key, nil
	}
	body, err := json.Marshal(SnapshotResponse{
		State:    state,
		Meta:     p.Meta(),
		Snapshot: stats.BuildSnapshot(p.Targets(), p.HTTPResults()),
	})
	if err != nil {
		return nil, snapshotKey{}, fmt.Errorf("encode snapshot: %w", err)
	}
	c.ok, c.key, c.body = true, key, body
	return body, key, nil
}
