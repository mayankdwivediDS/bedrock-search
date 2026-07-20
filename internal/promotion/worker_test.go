package promotion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-suggest-neo/internal/cache"
	"go-suggest-neo/internal/corpus"
	"go-suggest-neo/internal/ledger"
)

// buildTestCorpus creates a minimal bootstrapped corpus and returns a
// *corpus.Reader that points at it.
func buildTestCorpus(t *testing.T, words []string) *corpus.Reader {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src.json")
	raw, err := json.Marshal(words)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "v1")
	if _, err := corpus.Bootstrap(corpus.BootstrapOptions{
		SourceJSON: src, OutDir: outDir,
		SortChunkMB: 1, SkipStride: 8, Version: "v1", Mode: "bootstrap",
	}); err != nil {
		t.Fatal(err)
	}
	r, err := corpus.Open(
		filepath.Join(outDir, corpus.FileCorpusSorted),
		filepath.Join(outDir, corpus.FileCorpusIdx),
		64,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestWorker_EnqueueAndBuild(t *testing.T) {
	r := buildTestCorpus(t, []string{
		"market", "marketing", "marketplace", "marine", "mango",
		"avocado", "apple", "apricot",
	})
	c := cache.New(100)
	l := ledger.New(ledger.Config{Window: time.Minute, Threshold: 3, IdleTTL: time.Hour})
	w := New(Config{QueueSize: 8, Workers: 1, MaxWords: 100}, r, c, l)
	w.Start()
	defer w.Stop()

	if !w.Enqueue("mar") {
		t.Fatal("first Enqueue should succeed")
	}
	// Enqueue same prefix again — should be deduped (returns false without
	// counting as dropped).
	if w.Enqueue("mar") {
		t.Error("duplicate Enqueue should be deduped")
	}

	// Wait up to 1s for the worker to finish the build.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c.Has("mar") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Has("mar") {
		t.Fatal("promotion did not complete in time")
	}

	e, ok := c.Lookup("marketing")
	if !ok || e.Prefix != "mar" {
		t.Errorf("expected longest-prefix match on 'mar', got %v/%v", e, ok)
	}
	// The promoted trie must actually contain the matching words.
	hits := e.Trie.SearchOpts("mar", 10, false, false)
	if len(hits) == 0 {
		t.Error("promoted trie returned no results for 'mar'")
	}
}

func TestWorker_EmptyPrefixClearsPromoted(t *testing.T) {
	r := buildTestCorpus(t, []string{"apple", "banana"})
	c := cache.New(100)
	l := ledger.New(ledger.Config{Window: time.Minute, Threshold: 1, IdleTTL: time.Hour})
	w := New(Config{QueueSize: 8, Workers: 1, MaxWords: 100}, r, c, l)
	w.Start()
	defer w.Stop()

	// Mark "zz" as promoted then enqueue it. Since no words match, the
	// build path should ClearPromoted instead of inserting an empty trie.
	l.MarkPromoted("zz")
	w.Enqueue("zz")

	// Drain.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if w.Stats().Built > 0 || w.Stats().Enqueued > 0 && w.Stats().QueueDepth == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if c.Has("zz") {
		t.Error("cache should not hold entries for empty prefixes")
	}
	hot := l.HotPrefixes()
	for _, p := range hot {
		if p == "zz" {
			t.Error("ledger should have cleared promoted flag for empty-prefix build")
		}
	}
}

func TestWorker_FullQueueDrops(t *testing.T) {
	r := buildTestCorpus(t, []string{"apple", "banana"})
	c := cache.New(100)
	l := ledger.New(ledger.Config{Window: time.Minute, Threshold: 1, IdleTTL: time.Hour})
	// Queue size 1, no workers started → every Enqueue after the first
	// drops.
	w := New(Config{QueueSize: 1, Workers: 1, MaxWords: 100}, r, c, l)
	// NOTE: not calling Start — we want the queue to fill up.

	if !w.Enqueue("a") {
		t.Fatal("first Enqueue should succeed")
	}
	if w.Enqueue("b") {
		t.Fatal("second Enqueue into full queue should be dropped")
	}
	s := w.Stats()
	if s.Dropped != 1 {
		t.Errorf("expected 1 drop, got %d", s.Dropped)
	}
}
