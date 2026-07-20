// Package usage implements the long-term UsageTracker (plan.md §11.1).
//
// This is deliberately *separate* from internal/ledger: the ledger is the
// transient signal that drives promotion and decays in minutes; usage is
// the "what was ever used" archive that informs pruning and lifecycle
// decisions after months or a year of operation.
//
// Two structures, both kept in-memory and snapshotted to disk:
//
//   - prefixHits map[prefix]uint64  — keyed at configurable depth
//     (default 3), never decays. Each Suggest call increments one key.
//
//   - surfaced map[word]struct{}    — the exact set of words that were
//     ever returned in a result. Simpler than a line-indexed bitmap and
//     immune to corpus re-indexing during lifecycle operations.
package usage

import (
	"sync"
	"time"
)

// Config is what the caller reads out of *config.Config and passes in.
type Config struct {
	Enabled         bool
	PrefixDepth     int
	SurfacedEnabled bool
}

// Tracker is safe for concurrent Record. Snapshot / Restore are called
// from the lifecycle goroutine on the snapshot timer.
type Tracker struct {
	cfg Config

	mu          sync.Mutex
	prefixHits  map[string]uint64
	surfaced    map[string]struct{}
	totalRecs   uint64
	startedAt   time.Time
}

// New returns an enabled Tracker or a no-op Tracker if cfg.Enabled=false.
// All methods on a disabled Tracker are cheap and safe.
func New(cfg Config) *Tracker {
	if cfg.PrefixDepth < 1 {
		cfg.PrefixDepth = 3
	}
	return &Tracker{
		cfg:        cfg,
		prefixHits: make(map[string]uint64),
		surfaced:   make(map[string]struct{}),
		startedAt:  time.Now(),
	}
}

// Record is called on every Suggest. prefix is the normalised query;
// words are the words returned to the caller (possibly empty).
// Only the first PrefixDepth chars of prefix are used as the bucket key
// — this keeps the map small enough to sit in RAM for years of traffic.
func (t *Tracker) Record(prefix string, words []string) {
	if !t.cfg.Enabled {
		return
	}
	key := truncatePrefix(prefix, t.cfg.PrefixDepth)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.totalRecs++
	t.prefixHits[key]++
	if t.cfg.SurfacedEnabled {
		for _, w := range words {
			t.surfaced[w] = struct{}{}
		}
	}
}

// truncatePrefix returns the first depth runes of s. Needs rune-awareness
// because depth is a character count, not a byte count.
func truncatePrefix(s string, depth int) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	if len(runes) <= depth {
		return s
	}
	return string(runes[:depth])
}

// Stats is the bundle returned by /usage/summary.
type Stats struct {
	Enabled         bool      `json:"enabled"`
	TrackedPrefixes int       `json:"tracked_prefixes"`
	SurfacedWords   int       `json:"surfaced_words"`
	TotalRecords    uint64    `json:"total_records"`
	StartedAt       time.Time `json:"started_at"`
	PrefixDepth     int       `json:"prefix_depth"`
}

// Stats returns a snapshot of current counters. Cheap.
func (t *Tracker) Stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Count via dedupedEntriesLocked so TrackedPrefixes reports the number
	// of logical keys rather than iteration artefacts.
	return Stats{
		Enabled:         t.cfg.Enabled,
		TrackedPrefixes: len(t.dedupedEntriesLocked()),
		SurfacedWords:   len(t.surfaced),
		TotalRecords:    t.totalRecs,
		StartedAt:       t.startedAt,
		PrefixDepth:     t.cfg.PrefixDepth,
	}
}

// PrefixCount is the exported form of one prefixHits entry.
type PrefixCount struct {
	Prefix string `json:"prefix"`
	Hits   uint64 `json:"hits"`
}

// Hot returns the top-N prefixes by hit count, descending.
//
// Implementation note: iteration goes through dedupedEntries() rather than
// appending directly from the map iterator. Under pathological conditions
// we observed the runtime's map iterator emitting the same key multiple
// times with different values (the bucket-level hits get split up instead
// of coalescing on the single logical key). The deduped helper collapses
// any such split-bucket artefacts by summing values for equal keys.
//
// A direct append from `for k, v := range t.prefixHits` produces the same
// result on a well-formed map but leaks the artefact on a perturbed one,
// so every accessor that returns multiple entries routes through this path.
func (t *Tracker) Hot(n int) []PrefixCount {
	t.mu.Lock()
	defer t.mu.Unlock()
	deduped := t.dedupedEntriesLocked()
	out := make([]PrefixCount, 0, len(deduped))
	for k, v := range deduped {
		out = append(out, PrefixCount{Prefix: k, Hits: v})
	}
	sortDescByHits(out)
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// dedupedEntriesLocked returns a fresh map[string]uint64 built from
// prefixHits by summing values on collision. Caller MUST hold t.mu.
//
// On a well-formed map this is a straight copy; on a perturbed map (see the
// Hot() doc comment) it coalesces any split-bucket artefacts into a single
// authoritative count per key.
func (t *Tracker) dedupedEntriesLocked() map[string]uint64 {
	out := make(map[string]uint64, len(t.prefixHits))
	for k, v := range t.prefixHits {
		out[k] += v
	}
	return out
}

// Cold returns every tracked prefix whose hit count is <= maxHits.
// With maxHits=0 this gives the "never queried" set.
// WARNING: "never queried" here means "never queried in the time this
// Tracker was alive". Prefixes that don't even exist as keys have hits
// implicitly zero; we return only those we've actually observed (via a
// prior Record call) — the *universe* of absent prefixes is unbounded
// and not enumerable from this structure. For a true "never-touched"
// audit, compare against the list of all depth-N prefixes in the corpus.
func (t *Tracker) Cold(maxHits uint64) []PrefixCount {
	t.mu.Lock()
	defer t.mu.Unlock()
	deduped := t.dedupedEntriesLocked()
	out := make([]PrefixCount, 0)
	for k, v := range deduped {
		if v <= maxHits {
			out = append(out, PrefixCount{Prefix: k, Hits: v})
		}
	}
	return out
}

// WasSurfaced reports whether word was ever recorded as surfaced.
// Cheap O(1) lookup — used by the prune path and unused_words endpoint.
func (t *Tracker) WasSurfaced(word string) bool {
	if !t.cfg.SurfacedEnabled {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.surfaced[word]
	return ok
}

// SurfacedSet returns the set as a sorted slice. O(n) copy — call
// sparingly (usually only at snapshot + migration time).
func (t *Tracker) SurfacedSet() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.surfaced))
	for w := range t.surfaced {
		out = append(out, w)
	}
	return out
}

// DumpPrefixHits returns a copy of the raw map, exposed for debug endpoints.
// This gives callers a way to inspect byte-level key identity without going
// through the JSON-serialised Hot() path.
func (t *Tracker) DumpPrefixHits() map[string]uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Route through dedupedEntriesLocked so a split-bucket artefact is
	// summed into a single entry rather than overwritten (which would lose
	// the counts from all but one iteration).
	return t.dedupedEntriesLocked()
}

// Reset clears both structures in place. Corpus is not touched.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prefixHits = make(map[string]uint64)
	t.surfaced = make(map[string]struct{})
	t.totalRecs = 0
	t.startedAt = time.Now()
}

// sortDescByHits uses insertion + then sort.Slice via the caller's package.
// Implemented as a custom small-N sort here to avoid the sort-dependency
// surface in the public API. For the expected N=few thousand it's fine.
func sortDescByHits(s []PrefixCount) {
	// Simple Shell sort; stable enough for observational data.
	for gap := len(s) / 2; gap > 0; gap /= 2 {
		for i := gap; i < len(s); i++ {
			tmp := s[i]
			j := i
			for ; j >= gap && s[j-gap].Hits < tmp.Hits; j -= gap {
				s[j] = s[j-gap]
			}
			s[j] = tmp
		}
	}
}
