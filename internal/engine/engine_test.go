package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-suggest-neo/internal/config"
	"go-suggest-neo/internal/corpus"
)

// testEngine builds a throw-away engine against an in-memory corpus.
func testEngine(t *testing.T, words []string, overrides func(*config.Config)) (*Engine, *corpus.Reader, string) {
	t.Helper()

	src := filepath.Join(t.TempDir(), "src.json")
	raw, _ := json.Marshal(words)
	if err := os.WriteFile(src, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	listDir := t.TempDir()
	verDir := filepath.Join(listDir, "versions", "v1")
	if _, err := corpus.Bootstrap(corpus.BootstrapOptions{
		SourceJSON: src, OutDir: verDir,
		SortChunkMB: 1, SkipStride: 16, Version: "v1", Mode: "bootstrap",
	}); err != nil {
		t.Fatal(err)
	}
	r, err := corpus.Open(
		filepath.Join(verDir, corpus.FileCorpusSorted),
		filepath.Join(verDir, corpus.FileCorpusIdx),
		64,
	)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		MinQueryLen:               2,
		MaxQueryLatencyMs:         200,
		DefaultLimit:              10,
		MaxLimit:                  100,
		WordCap:                   1000,
		PromotionThreshold:        2, // low so the tests fire promotion quickly
		PromotionWindowSec:        60,
		PromotionMaxWords:         100,
		PromotionQueueSize:        16,
		PromotionWorkers:          1,
		LedgerIdleTTLSec:          600,
		LedgerSnapshotIntervalSec: 60,
		PinnedWarmupEnabled:       false, // engine_test doesn't care about warmup by default
		PinnedWarmupTimeoutSec:    5,
		UsageTrackerEnabled:       false,
	}
	if overrides != nil {
		overrides(cfg)
	}
	e := New(cfg, r, listDir, verDir)
	return e, r, listDir
}

func TestSuggest_ColdMissThenPromotionMakesNextHit(t *testing.T) {
	words := []string{"market", "marketing", "marketplace", "marine", "mango", "apple"}
	e, r, _ := testEngine(t, words, nil)
	defer r.Close()
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Stop(context.Background())

	// First query — cold miss, reader serves it from disk.
	results, err := e.Suggest(context.Background(), "mar", 10, false)
	if err != nil {
		t.Fatalf("suggest 1: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected cold results")
	}
	for _, r := range results {
		if r.Source != "cold" {
			t.Errorf("first query should be cold, got %s", r.Source)
		}
	}

	// Second query — ledger threshold (=2) crossed, promotion triggered.
	_, err = e.Suggest(context.Background(), "mar", 10, false)
	if err != nil {
		t.Fatalf("suggest 2: %v", err)
	}

	// Wait for promotion worker to finish.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if e.Cache().Has("mar") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !e.Cache().Has("mar") {
		t.Fatal("promotion did not complete")
	}

	// Third query — should now hit the cache.
	results, err = e.Suggest(context.Background(), "mar", 10, false)
	if err != nil {
		t.Fatalf("suggest 3: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected hot results")
	}
	for _, r := range results {
		if r.Source != "hot" {
			t.Errorf("third query should be hot, got %s", r.Source)
		}
	}
}

func TestSuggest_RejectsQueryBelowMinLen(t *testing.T) {
	e, r, _ := testEngine(t, []string{"apple"}, nil)
	defer r.Close()
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Stop(context.Background())

	_, err := e.Suggest(context.Background(), "a", 10, false)
	if err == nil {
		t.Error("expected min-length error, got nil")
	}
}

func TestSuggest_CancelledContextReturnsPartial(t *testing.T) {
	words := []string{"apple", "apricot", "avocado"}
	e, r, _ := testEngine(t, words, nil)
	defer r.Close()
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Stop(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	results, err := e.Suggest(ctx, "ap", 10, false)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	// Either 0 results (cancelled before reading) or partial — both are
	// acceptable; what matters is no error propagates.
	_ = results
}

func TestWarmPinned_SynchronouslyPopulatesCache(t *testing.T) {
	words := []string{"market", "marketing", "marketplace", "mango", "apple"}
	e, r, listDir := testEngine(t, words, func(c *config.Config) {
		c.PinnedWarmupEnabled = true
	})
	defer r.Close()

	pinned := filepath.Join(listDir, "pinned_prefixes.txt")
	if err := os.WriteFile(pinned, []byte("mar\n# comment line\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Stop(context.Background())

	// Pinned warmup is synchronous — cache must already have "mar" before
	// Start returns.
	if !e.Cache().Has("mar") {
		t.Fatal("pinned warmup did not populate cache synchronously")
	}

	// First query should be a hot hit (no promotion needed).
	results, err := e.Suggest(context.Background(), "mar", 10, false)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	for _, r := range results {
		if r.Source != "hot" {
			t.Errorf("expected hot after pinned warmup, got %s", r.Source)
		}
	}
}

func TestSnapshotWrittenOnStop(t *testing.T) {
	e, r, listDir := testEngine(t, []string{"alpha", "bravo"}, nil)
	defer r.Close()
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	e.Suggest(context.Background(), "al", 10, false)
	e.Suggest(context.Background(), "al", 10, false)
	e.Stop(context.Background())

	// Snapshot should exist on disk.
	snap := filepath.Join(listDir, "ledger.snapshot.gob")
	info, err := os.Stat(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	if info.Size() == 0 {
		t.Error("snapshot is empty")
	}
}
