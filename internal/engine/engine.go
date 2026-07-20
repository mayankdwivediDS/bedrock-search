// Package engine is the orchestrator. It owns the single CorpusReader +
// HotTrieCache + QueryLedger + promotion.Worker for a running server and
// exposes one public query API: Suggest.
//
// Lifecycle:
//
//	e := engine.New(cfg, reader)
//	if err := e.Start(ctx); err != nil { ... }   // load snapshot, pinned warmup
//	defer e.Stop(ctx)                             // flush snapshot, stop worker
//	results, err := e.Suggest(ctx, "marketi", 10, false)
//
// All HTTP handlers go through Suggest; no handler touches the cache,
// ledger, or reader directly. That keeps the contract narrow and the 200 ms
// latency ceiling enforceable in exactly one place (§9b of plan.md).
package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go-suggest-neo/internal/cache"
	"go-suggest-neo/internal/config"
	"go-suggest-neo/internal/corpus"
	"go-suggest-neo/internal/ledger"
	"go-suggest-neo/internal/normalise"
	"go-suggest-neo/internal/promotion"
	"go-suggest-neo/internal/trie"
	"go-suggest-neo/internal/usage"
)

// Engine bundles every runtime subsystem for one list.
// Multi-list support is a thin wrapper around a map[listName]*Engine and
// arrives with phase 9.
type Engine struct {
	cfg *config.Config

	reader *corpus.Reader
	cache  *cache.Cache
	ledger *ledger.Ledger
	worker *promotion.Worker
	usage  *usage.Tracker

	// Paths used by snapshot + pinned warmup. Derived from cfg and listDir.
	ledgerSnapshotPath string
	pinnedPrefixesPath string
	usageSnapshotPath  string
	// verDir is the active corpus version directory (…/versions/vN/). Used
	// to derive per-version artefacts like usage.stats.gob.
	verDir string

	// Lifecycle plumbing.
	stopCh    chan struct{}
	bgWG      sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
}

// New constructs an Engine. Caller must Start before serving traffic.
// listDir is the per-list directory, e.g. <data>/default — used to derive
// ledger snapshot + pinned paths when the env vars are empty.
// verDir is the current corpus version directory, used for the per-version
// usage.stats.gob.
func New(cfg *config.Config, r *corpus.Reader, listDir, verDir string) *Engine {
	c := cache.New(cfg.WordCap)
	l := ledger.New(ledger.Config{
		Window:    time.Duration(cfg.PromotionWindowSec) * time.Second,
		Threshold: uint32(cfg.PromotionThreshold),
		IdleTTL:   time.Duration(cfg.LedgerIdleTTLSec) * time.Second,
	})
	w := promotion.New(promotion.Config{
		QueueSize: cfg.PromotionQueueSize,
		Workers:   cfg.PromotionWorkers,
		MaxWords:  cfg.PromotionMaxWords,
	}, r, c, l)
	u := usage.New(usage.Config{
		Enabled:         cfg.UsageTrackerEnabled,
		PrefixDepth:     cfg.UsagePrefixDepth,
		SurfacedEnabled: cfg.UsageSurfacedEnabled,
	})

	e := &Engine{
		cfg:    cfg,
		reader: r,
		cache:  c,
		ledger: l,
		worker: w,
		usage:  u,
		verDir: verDir,
		stopCh: make(chan struct{}),
	}
	e.ledgerSnapshotPath = cfg.LedgerSnapshotPath
	if e.ledgerSnapshotPath == "" {
		e.ledgerSnapshotPath = filepath.Join(listDir, "ledger.snapshot.gob")
	}
	e.pinnedPrefixesPath = cfg.PinnedPrefixesFile
	if e.pinnedPrefixesPath == "" {
		e.pinnedPrefixesPath = filepath.Join(listDir, "pinned_prefixes.txt")
	}
	e.usageSnapshotPath = filepath.Join(verDir, corpus.FileUsageStats)
	return e
}

// Start performs mitigations A + B from plan.md §9a:
//
//	A: load ledger snapshot → async-promote prefixes that were hot last run.
//	B: synchronously promote pinned prefixes BEFORE the caller opens the
//	   HTTP port, so the very first request already has a warm cache.
//
// Also kicks off the worker and the background timers (sweep + snapshot).
//
// Returns an error only on conditions the caller can meaningfully react to
// (e.g. invalid pinned file). Missing snapshot is fine — it just means
// cold restart.
func (e *Engine) Start(ctx context.Context) error {
	var retErr error
	e.startOnce.Do(func() {
		e.worker.Start()

		// Restore usage tracker from per-version snapshot if present.
		if snap, err := usage.Read(e.usageSnapshotPath); err != nil {
			slog.Warn("usage snapshot read failed (continuing fresh)",
				"path", e.usageSnapshotPath, "err", err)
		} else if snap != nil {
			usage.Restore(e.usage, snap)
			slog.Info("usage snapshot restored",
				"prefixes", snap.PrefixHits, "surfaced", len(snap.Surfaced))
		}

		// A: load snapshot and async-enqueue any previously-hot prefixes.
		if entries, err := ledger.ReadSnapshot(e.ledgerSnapshotPath); err != nil {
			slog.Warn("ledger snapshot read failed (continuing cold)",
				"path", e.ledgerSnapshotPath, "err", err)
		} else if entries != nil {
			loaded := e.ledger.Restore(entries)
			slog.Info("ledger snapshot restored", "entries_kept", loaded)
			for _, p := range e.ledger.HotPrefixes() {
				// Enqueue is non-blocking; if the queue is full we'll get
				// the rest on subsequent traffic.
				e.worker.Enqueue(p)
			}
		}

		// B: synchronous pinned-prefix warmup.
		if e.cfg.PinnedWarmupEnabled {
			if err := e.warmPinned(ctx); err != nil {
				retErr = err
				return
			}
		}

		// Background timers.
		e.bgWG.Add(3)
		go e.runLedgerSweepLoop()
		go e.runLedgerSnapshotLoop()
		go e.runUsageSnapshotLoop()
	})
	return retErr
}

// Stop flushes the ledger snapshot (best-effort) and shuts down the worker
// and background timers. Safe to call multiple times; only the first has
// effect.
func (e *Engine) Stop(ctx context.Context) {
	e.stopOnce.Do(func() {
		close(e.stopCh)
		e.bgWG.Wait()
		e.worker.Stop()
		// Final snapshot flush.
		if err := ledger.WriteSnapshot(e.ledger, e.ledgerSnapshotPath); err != nil {
			slog.Warn("final ledger snapshot failed", "err", err)
		} else {
			slog.Info("ledger snapshot written", "path", e.ledgerSnapshotPath)
		}
		if err := usage.Write(e.usage, e.usageSnapshotPath); err != nil {
			slog.Warn("final usage snapshot failed", "err", err)
		} else {
			slog.Info("usage snapshot written", "path", e.usageSnapshotPath)
		}
	})
}

// runUsageSnapshotLoop mirrors runLedgerSnapshotLoop for the usage tracker.
// Usage is written less often (default 5 min) because the tracker grows
// monotonically between snapshots and a skipped tick just delays visibility.
func (e *Engine) runUsageSnapshotLoop() {
	defer e.bgWG.Done()
	interval := time.Duration(e.cfg.UsageSnapshotIntervalSec) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-t.C:
			if err := usage.Write(e.usage, e.usageSnapshotPath); err != nil {
				slog.Warn("periodic usage snapshot failed", "err", err)
			}
		}
	}
}

// Result is one suggestion returned by Suggest.
type Result struct {
	Word   string `json:"word"`
	Source string `json:"source"` // "hot" | "cold"
}

// Suggest is the single query entry point. It enforces MinQueryLen, records
// into the ledger, tries longest-prefix match against the HotTrieCache, and
// falls back to a bounded cold-path disk scan. Every caller gets the same
// 200 ms context.Deadline via WithTimeout (see the per-request Middleware
// in internal/server if you need to override it).
//
// fuzzy=true only affects hot hits — on a cold miss we currently serve
// prefix results only (plan.md §9b rule 1). Promotion of the prefix will
// happen once the ledger threshold is crossed, at which point fuzzy becomes
// available on the next query.
func (e *Engine) Suggest(ctx context.Context, rawQuery string, limit int, fuzzy bool) ([]Result, error) {
	q := normalise.String(rawQuery)
	if len(q) < e.cfg.MinQueryLen {
		return nil, fmt.Errorf("query must be at least %d characters", e.cfg.MinQueryLen)
	}
	if limit <= 0 {
		limit = e.cfg.DefaultLimit
	}
	if limit > e.cfg.MaxLimit {
		limit = e.cfg.MaxLimit
	}

	// Record into the ledger; if this call crossed the threshold, enqueue
	// for promotion (non-blocking; drop is fine).
	if crossed := e.ledger.Record(q); crossed {
		e.worker.Enqueue(q)
	}

	// Longest-prefix cache lookup.
	if entry, ok := e.cache.Lookup(q); ok {
		hits := entry.Trie.SearchOpts(q, limit, fuzzy, fuzzy)
		e.usage.Record(q, hits)
		out := make([]Result, 0, len(hits))
		for _, w := range hits {
			out = append(out, Result{Word: w, Source: "hot"})
		}
		return out, nil
	}

	// Cold path — bounded disk read.
	hits, err := e.reader.PrefixScan(ctx, q, limit)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			// Return partial results rather than an error (plan.md §9b).
			e.usage.Record(q, hits)
			out := make([]Result, 0, len(hits))
			for _, w := range hits {
				out = append(out, Result{Word: w, Source: "cold"})
			}
			return out, nil
		}
		return nil, err
	}
	e.usage.Record(q, hits)
	out := make([]Result, 0, len(hits))
	for _, w := range hits {
		out = append(out, Result{Word: w, Source: "cold"})
	}
	return out, nil
}

// Usage returns the UsageTracker. Exposed so /usage/* handlers can read.
func (e *Engine) Usage() *usage.Tracker { return e.usage }

// FlushUsageSnapshot writes the current in-memory usage tracker to disk
// immediately. Normally the snapshot timer handles this (default 5 min);
// operators call this before /corpus/apply?mode=prune so the prune sees
// the freshest possible usage data rather than waiting for the next tick.
func (e *Engine) FlushUsageSnapshot() error {
	return usage.Write(e.usage, e.usageSnapshotPath)
}

// FlushLedgerSnapshot is the ledger equivalent of FlushUsageSnapshot.
// Useful for operators who want a fresh ledger snapshot captured before
// a restart outside the normal 60 s cadence.
func (e *Engine) FlushLedgerSnapshot() error {
	return ledger.WriteSnapshot(e.ledger, e.ledgerSnapshotPath)
}

// VerDir returns the current version directory (e.g. …/versions/v7). Used
// by the lifecycle package to locate per-version artefacts.
func (e *Engine) VerDir() string { return e.verDir }

// runLedgerSweepLoop drops idle counters every LedgerIdleTTL/4 (dense
// enough to keep memory bounded, sparse enough to avoid lock thrash).
func (e *Engine) runLedgerSweepLoop() {
	defer e.bgWG.Done()
	interval := time.Duration(e.cfg.LedgerIdleTTLSec) * time.Second / 4
	if interval < time.Second {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-t.C:
			if n := e.ledger.Sweep(); n > 0 {
				slog.Debug("ledger sweep", "evicted", n)
			}
		}
	}
}

// runLedgerSnapshotLoop writes ledger.snapshot.gob at the configured
// interval. Errors are logged but not fatal — a skipped snapshot just means
// a colder restart.
func (e *Engine) runLedgerSnapshotLoop() {
	defer e.bgWG.Done()
	interval := time.Duration(e.cfg.LedgerSnapshotIntervalSec) * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-t.C:
			if err := ledger.WriteSnapshot(e.ledger, e.ledgerSnapshotPath); err != nil {
				slog.Warn("periodic ledger snapshot failed", "err", err)
			}
		}
	}
}

// warmPinned loads pinned_prefixes.txt and synchronously promotes each
// prefix. The synchronous property is what makes "no cold hits for pinned
// paths even on first query" true (plan.md §9a mitigation B).
//
// Does NOT go through the worker queue — those jobs are async, and we want
// the HTTP port to stay closed until warmup is done.
func (e *Engine) warmPinned(ctx context.Context) error {
	prefixes, err := readPinnedPrefixes(e.pinnedPrefixesPath)
	if err != nil {
		return fmt.Errorf("read pinned: %w", err)
	}
	if len(prefixes) == 0 {
		return nil
	}

	timeout := time.Duration(e.cfg.PinnedWarmupTimeoutSec) * time.Second
	warmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	promoted := 0
	for _, p := range prefixes {
		if warmCtx.Err() != nil {
			slog.Warn("pinned warmup timeout",
				"timeout", timeout,
				"completed", promoted, "pending", len(prefixes)-promoted)
			break
		}
		words, err := e.reader.PrefixScanAll(warmCtx, p, e.cfg.PromotionMaxWords)
		if err != nil {
			slog.Warn("pinned warmup scan failed", "prefix", p, "err", err)
			continue
		}
		if len(words) == 0 {
			slog.Debug("pinned prefix has no words", "prefix", p)
			continue
		}
		t := trie.New().WithoutOriginalDict()
		t.Insert(words...)
		e.cache.Insert(p, t, len(words))
		e.ledger.MarkPromoted(p)
		promoted++
	}
	slog.Info("pinned warmup complete",
		"promoted", promoted, "pinned_total", len(prefixes),
		"elapsed", time.Since(start))
	return nil
}

// readPinnedPrefixes parses pinned_prefixes.txt. Format: one prefix per
// line, # starts a comment, blank lines ignored. A missing file is not an
// error (returns nil, nil).
func readPinnedPrefixes(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n := normalise.String(line)
		if n != "" {
			out = append(out, n)
		}
	}
	return out, sc.Err()
}

// Cache returns the underlying HotTrieCache. Exposed for /cache_stats
// (phase 8).
func (e *Engine) Cache() *cache.Cache { return e.cache }

// Ledger returns the underlying QueryLedger. Exposed for /cache_stats.
func (e *Engine) Ledger() *ledger.Ledger { return e.ledger }

// Worker returns the underlying promotion worker. Exposed for /cache_stats.
func (e *Engine) Worker() *promotion.Worker { return e.worker }

// Reader returns the underlying corpus reader. Exposed for /cache_stats
// and for lifecycle operations that need corpus metadata.
func (e *Engine) Reader() *corpus.Reader { return e.reader }
