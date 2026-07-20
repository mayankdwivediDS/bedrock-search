// Package promotion owns the async pipeline that builds a per-prefix trie
// from disk and installs it into the HotTrieCache.
//
// Contract:
//
//   - Enqueue is non-blocking (channel-backed, fixed capacity). If the queue
//     is full the request is dropped — the prefix will get another chance the
//     next time it crosses the ledger threshold.
//   - Each Worker goroutine consumes one prefix at a time, reads up to
//     MaxWords matching words from disk, builds a trie, and calls
//     Cache.Insert. Eviction happens inside Cache.
//   - The promotion context has no deadline — promotion is expected to take
//     tens to hundreds of ms and must not interact with the 200 ms query
//     ceiling. Workers stop when Stop() is called.
//
// This is the "cold miss → hot" arm of the self-learning loop in plan.md §3.
package promotion

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go-suggest-neo/internal/cache"
	"go-suggest-neo/internal/corpus"
	"go-suggest-neo/internal/ledger"
	"go-suggest-neo/internal/trie"
)

// Config bundles the promotion tunables.
type Config struct {
	QueueSize int // PROMOTION_QUEUE_SIZE
	Workers   int // PROMOTION_WORKERS
	MaxWords  int // PROMOTION_MAX_WORDS
}

// Worker is the promotion subsystem. Start kicks off the goroutines; Stop
// closes the channel and waits for them to drain.
type Worker struct {
	cfg    Config
	reader *corpus.Reader
	cache  *cache.Cache
	ledger *ledger.Ledger

	queue chan string
	wg    sync.WaitGroup
	// dedupe keeps track of prefixes currently in the queue OR being built,
	// so burst traffic on the same prefix doesn't spawn N identical jobs.
	dedupMu sync.Mutex
	dedup   map[string]struct{}

	// Stats.
	enqueued   uint64
	dropped    uint64
	built      uint64
	failed     uint64
	buildMicro uint64 // sum of build durations in microseconds
}

// New constructs a Worker. The caller must call Start exactly once and
// Stop on shutdown.
func New(cfg Config, r *corpus.Reader, c *cache.Cache, l *ledger.Ledger) *Worker {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.QueueSize < 1 {
		cfg.QueueSize = 64
	}
	return &Worker{
		cfg:    cfg,
		reader: r,
		cache:  c,
		ledger: l,
		queue:  make(chan string, cfg.QueueSize),
		dedup:  make(map[string]struct{}),
	}
}

// Start launches the worker goroutines. Safe to call exactly once.
func (w *Worker) Start() {
	for i := 0; i < w.cfg.Workers; i++ {
		w.wg.Add(1)
		go w.loop()
	}
}

// Stop closes the queue and waits for workers to drain whatever is in flight.
// Callers should drain before closing the underlying corpus.Reader.
func (w *Worker) Stop() {
	close(w.queue)
	w.wg.Wait()
}

// Enqueue is the non-blocking trigger. Returns true if accepted, false if
// the queue is full (the caller can surface that via metrics but should NOT
// retry — the next Crossed signal will fire again shortly).
//
// Dedupes: if a prefix is already queued or being processed, returns false
// without counting it as dropped.
func (w *Worker) Enqueue(prefix string) bool {
	if prefix == "" {
		return false
	}
	w.dedupMu.Lock()
	if _, pending := w.dedup[prefix]; pending {
		w.dedupMu.Unlock()
		return false
	}
	// Also dedupe against prefixes already resident in the cache — if it's
	// there, promotion is pointless.
	if w.cache.Has(prefix) {
		w.dedupMu.Unlock()
		return false
	}
	w.dedup[prefix] = struct{}{}
	w.dedupMu.Unlock()

	select {
	case w.queue <- prefix:
		w.enqueued++
		return true
	default:
		// Channel full — undo the dedupe entry so a later Enqueue can try.
		w.dedupMu.Lock()
		delete(w.dedup, prefix)
		w.dedupMu.Unlock()
		w.dropped++
		return false
	}
}

func (w *Worker) loop() {
	defer w.wg.Done()
	for prefix := range w.queue {
		w.build(prefix)
		w.dedupMu.Lock()
		delete(w.dedup, prefix)
		w.dedupMu.Unlock()
	}
}

// build reads the full prefix range from disk, constructs a trie, and
// installs it. Runs without a deadline — promotion is allowed to take
// significant time.
func (w *Worker) build(prefix string) {
	start := time.Now()
	// Context.Background here is deliberate: we want the promotion to finish
	// regardless of how long it takes. Upper bound is MaxWords.
	words, err := w.reader.PrefixScanAll(context.Background(), prefix, w.cfg.MaxWords)
	if err != nil {
		w.failed++
		slog.Warn("promotion scan failed", "prefix", prefix, "err", err)
		return
	}
	if len(words) == 0 {
		// Nothing matches — don't insert an empty trie, but do flag it as
		// not-hot so the ledger's promoted bit resets and a future burst
		// can re-try.
		w.ledger.ClearPromoted(prefix)
		return
	}
	t := trie.New().WithoutOriginalDict()
	t.Insert(words...)
	evicted := w.cache.Insert(prefix, t, len(words))
	// When a prefix is evicted, clear its promoted flag so the next burst
	// can re-trigger a promotion.
	for _, ev := range evicted {
		w.ledger.ClearPromoted(ev)
	}
	w.built++
	w.buildMicro += uint64(time.Since(start).Microseconds())
	slog.Debug("promoted",
		"prefix", prefix,
		"words", len(words),
		"evicted", len(evicted),
		"took_ms", time.Since(start).Milliseconds())
}

// Stats is a cheap observability bundle.
type Stats struct {
	Enqueued      uint64
	Dropped       uint64
	Built         uint64
	Failed        uint64
	AvgBuildMicro uint64
	QueueDepth    int
	InFlight      int
}

// Stats returns a snapshot of counters.
func (w *Worker) Stats() Stats {
	w.dedupMu.Lock()
	inFlight := len(w.dedup)
	w.dedupMu.Unlock()
	var avg uint64
	if w.built > 0 {
		avg = w.buildMicro / w.built
	}
	return Stats{
		Enqueued:      w.enqueued,
		Dropped:       w.dropped,
		Built:         w.built,
		Failed:        w.failed,
		AvgBuildMicro: avg,
		QueueDepth:    len(w.queue),
		InFlight:      inFlight,
	}
}
