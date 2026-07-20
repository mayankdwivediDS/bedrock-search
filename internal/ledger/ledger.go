// Package ledger owns the transient per-prefix hit counter used to decide
// when a prefix deserves promotion into the HotTrieCache.
//
// It is intentionally *different* from the long-term UsageTracker
// (internal/usage): ledger counters decay, usage counters never do.
// The ledger cares about "is this prefix hot *right now*"; the usage
// tracker cares about "has this prefix ever been used in the corpus'
// lifetime".
//
// Design per plan.md §2.3:
//
//   - map[prefix] → counter { hits, windowStart, lastSeen, promoted }
//   - A counter whose windowStart is older than Window is reset on next Record.
//   - Record returns a Crossed flag when hits reaches Threshold; this is the
//     trigger that the promotion worker subscribes to.
//   - Sweep drops counters with no activity for IdleTTL. Called periodically.
package ledger

import (
	"sync"
	"time"
)

// Config bundles the knobs the ledger reads.
type Config struct {
	Window    time.Duration // PROMOTION_WINDOW_SEC
	Threshold uint32        // PROMOTION_THRESHOLD
	IdleTTL   time.Duration // LEDGER_IDLE_TTL_SEC
}

// counter is the per-prefix state machine.
//
// windowStart + hits together implement a fixed-window rate counter (not a
// true sliding window, but cheaper and adequate for promotion decisions).
// When a new Record arrives after Window has elapsed, we reset hits and
// promoted to zero, then count fresh.
type counter struct {
	hits        uint32
	windowStart time.Time
	lastSeen    time.Time
	promoted    bool
}

// Ledger is safe for concurrent use. Record is called on the query hot path
// so it must be fast — we take a single mutex and do a constant-time map
// lookup + a few field writes.
type Ledger struct {
	mu     sync.Mutex
	cfg    Config
	counts map[string]*counter
	// Stats for observability.
	totalRecords uint64
	totalPromos  uint64
}

// New creates an empty Ledger.
func New(cfg Config) *Ledger {
	return &Ledger{cfg: cfg, counts: make(map[string]*counter)}
}

// Record increments the hit counter for prefix and returns Crossed=true on
// the exact call that first pushes the counter past Threshold within the
// current window. Subsequent calls in the same window do NOT return Crossed
// again — the caller only needs to promote once.
func (l *Ledger) Record(prefix string) (crossed bool) {
	if prefix == "" {
		return false
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.totalRecords++

	c, ok := l.counts[prefix]
	if !ok {
		c = &counter{windowStart: now}
		l.counts[prefix] = c
	}
	if now.Sub(c.windowStart) > l.cfg.Window {
		c.hits = 0
		c.windowStart = now
		c.promoted = false
	}
	c.hits++
	c.lastSeen = now
	if !c.promoted && c.hits >= l.cfg.Threshold {
		c.promoted = true
		l.totalPromos++
		return true
	}
	return false
}

// MarkPromoted lets the caller (usually boot warmup from snapshot) flag a
// prefix as already in-cache so the next Record doesn't re-fire the
// Crossed signal until the window resets.
func (l *Ledger) MarkPromoted(prefix string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.counts[prefix]
	if !ok {
		c = &counter{windowStart: now, lastSeen: now}
		l.counts[prefix] = c
	}
	c.promoted = true
}

// ClearPromoted flips the promoted flag back off for a prefix — used when
// the HotTrieCache evicts it, so the next burst of hits can re-promote.
func (l *Ledger) ClearPromoted(prefix string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.counts[prefix]; ok {
		c.promoted = false
	}
}

// Sweep drops counters whose lastSeen is older than IdleTTL. Called on a
// timer; returns how many entries were evicted (useful for /cache_stats).
func (l *Ledger) Sweep() int {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	removed := 0
	for k, c := range l.counts {
		if now.Sub(c.lastSeen) > l.cfg.IdleTTL {
			delete(l.counts, k)
			removed++
		}
	}
	return removed
}

// Size returns the current number of tracked prefixes.
func (l *Ledger) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.counts)
}

// Stats is a snapshot of lifetime counters.
type Stats struct {
	TrackedPrefixes int
	TotalRecords    uint64
	TotalPromotions uint64
}

// Stats returns a cheap observability bundle.
func (l *Ledger) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{
		TrackedPrefixes: len(l.counts),
		TotalRecords:    l.totalRecords,
		TotalPromotions: l.totalPromos,
	}
}

// SnapshotEntry is the gob-encodable form of one counter.
type SnapshotEntry struct {
	Prefix      string
	Hits        uint32
	WindowStart time.Time
	LastSeen    time.Time
	Promoted    bool
}

// Snapshot returns an allocation-free (beyond the result slice) copy of
// every counter for persistence. Caller typically gob-encodes the result
// to disk.
func (l *Ledger) Snapshot() []SnapshotEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]SnapshotEntry, 0, len(l.counts))
	for k, c := range l.counts {
		out = append(out, SnapshotEntry{
			Prefix:      k,
			Hits:        c.hits,
			WindowStart: c.windowStart,
			LastSeen:    c.lastSeen,
			Promoted:    c.promoted,
		})
	}
	return out
}

// Restore replaces the in-memory state from a previously-snapshotted slice.
// Used at boot. Entries whose LastSeen is older than IdleTTL are skipped
// (already "cold" by policy).
func (l *Ledger) Restore(entries []SnapshotEntry) int {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	loaded := 0
	for _, e := range entries {
		if now.Sub(e.LastSeen) > l.cfg.IdleTTL {
			continue
		}
		l.counts[e.Prefix] = &counter{
			hits:        e.Hits,
			windowStart: e.WindowStart,
			lastSeen:    e.LastSeen,
			promoted:    e.Promoted,
		}
		loaded++
	}
	return loaded
}

// HotPrefixes returns the prefixes from the current state whose promoted
// flag is set — used at boot to re-enqueue them for trie build.
func (l *Ledger) HotPrefixes() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0)
	for k, c := range l.counts {
		if c.promoted {
			out = append(out, k)
		}
	}
	return out
}
