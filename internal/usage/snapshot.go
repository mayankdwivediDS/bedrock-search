package usage

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"os"
	"time"
)

// Snapshot is the gob-encoded representation, kept separate from Tracker
// so we can evolve Tracker internals without invalidating every snapshot.
// Exported so internal/lifecycle can build a migrated snapshot directly
// without round-tripping through gob.
type Snapshot struct {
	Version     int
	PrefixHits  map[string]uint64
	Surfaced    []string
	TotalRecs   uint64
	StartedAt   time.Time
	PrefixDepth int
}

const snapshotVersion = 1

// Write dumps the current state to path atomically (tmp + rename).
func Write(t *Tracker, path string) error {
	t.mu.Lock()
	snap := Snapshot{
		Version:     snapshotVersion,
		PrefixHits:  make(map[string]uint64, len(t.prefixHits)),
		Surfaced:    make([]string, 0, len(t.surfaced)),
		TotalRecs:   t.totalRecs,
		StartedAt:   t.startedAt,
		PrefixDepth: t.cfg.PrefixDepth,
	}
	for k, v := range t.prefixHits {
		snap.PrefixHits[k] = v
	}
	for w := range t.surfaced {
		snap.Surfaced = append(snap.Surfaced, w)
	}
	t.mu.Unlock()

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	bw := bufio.NewWriterSize(f, 1<<16)
	if err := gob.NewEncoder(bw).Encode(&snap); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("gob encode: %w", err)
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Read loads a previously written snapshot. A missing file returns nil,nil
// (cold-start case); a corrupt file returns an error.
func Read(path string) (*Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var snap Snapshot
	if err := gob.NewDecoder(bufio.NewReader(f)).Decode(&snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// Restore populates t's state from a snapshot. Replaces existing state.
func Restore(t *Tracker, snap *Snapshot) {
	if snap == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prefixHits = snap.PrefixHits
	if t.prefixHits == nil {
		t.prefixHits = make(map[string]uint64)
	}
	t.surfaced = make(map[string]struct{}, len(snap.Surfaced))
	for _, w := range snap.Surfaced {
		t.surfaced[w] = struct{}{}
	}
	t.totalRecs = snap.TotalRecs
	if !snap.StartedAt.IsZero() {
		t.startedAt = snap.StartedAt
	}
}
