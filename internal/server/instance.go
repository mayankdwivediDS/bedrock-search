// Package server hosts the simplified HTTP surface for go-suggest-neo.
//
// The whole API is six routes:
//
//	GET  /health    liveness + corpus size + live version
//	GET  /suggest   the autocomplete query (engine logic unchanged)
//	POST /upload    add a CSV column to the corpus (dedup) and go live
//	POST /reload    rebuild the engine from disk, in-process
//	GET  /backup    download the whole data directory as one .zip
//	POST /restore   load a backup .zip and go live
//
// Instance is what makes /upload, /reload, and /restore work without an
// external process supervisor: it holds the live *engine.Engine behind an
// atomic pointer and swaps a freshly-built engine in on reload.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"go-suggest-neo/internal/config"
	"go-suggest-neo/internal/corpus"
	"go-suggest-neo/internal/engine"
	"go-suggest-neo/internal/lifecycle"
)

// Instance owns the live engine for one list and supports atomic in-process
// reloads. Read requests (/suggest, /health) call Engine() to grab the
// current engine; reloads build a new engine, swap the pointer, then retire
// the old one. No external supervisor is needed.
type Instance struct {
	cfg      *config.Config
	listName string
	listDir  string

	// mu serialises the heavy operations (upload / reload / restore) so two
	// version swaps never race. Read traffic never takes this lock.
	mu  sync.Mutex
	cur atomic.Pointer[engine.Engine]

	// retireWG tracks engines being shut down in the background. Restore
	// waits on it before moving the data directory, so no stale corpus file
	// is still open (which Windows would refuse to move).
	retireWG sync.WaitGroup
}

// NewInstance builds the first engine and returns a ready Instance. If no
// corpus exists yet it creates an empty one so the server can start and wait
// for the first /upload.
func NewInstance(cfg *config.Config, listName string) (*Instance, error) {
	inst := &Instance{
		cfg:      cfg,
		listName: listName,
		listDir:  filepath.Join(cfg.DataDir, listName),
	}
	if err := ensureCorpus(cfg, inst.listDir); err != nil {
		return nil, fmt.Errorf("ensure corpus: %w", err)
	}
	eng, err := buildEngine(cfg, inst.listDir)
	if err != nil {
		return nil, err
	}
	inst.cur.Store(eng)
	return inst, nil
}

// Engine returns the currently-live engine. Safe to call concurrently.
func (i *Instance) Engine() *engine.Engine { return i.cur.Load() }

// ListDir is the per-list data directory (<DATA_DIR>/<list>).
func (i *Instance) ListDir() string { return i.listDir }

// Paths returns the lifecycle paths for this instance's list.
func (i *Instance) Paths() lifecycle.Paths { return lifecycle.PathsFor(i.listDir) }

// Reload rebuilds the engine from whatever current.version points at and
// swaps it in atomically. The previous engine is retired after a short grace
// period so in-flight /suggest calls finish cleanly.
func (i *Instance) Reload(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.reloadLocked()
}

// reloadLocked is the lock-free body of Reload. Callers that already hold mu
// (upload, restore) use it directly.
func (i *Instance) reloadLocked() error {
	neu, err := buildEngine(i.cfg, i.listDir)
	if err != nil {
		return err
	}
	old := i.cur.Swap(neu)
	i.retire(old)
	return nil
}

// Stop retires the live engine (flush snapshots, close the corpus file).
// Called on graceful shutdown.
func (i *Instance) Stop(ctx context.Context) {
	if e := i.cur.Load(); e != nil {
		e.Stop(ctx)
		_ = e.Reader().Close()
	}
}

// retire stops an engine and closes its corpus file after a grace period, so
// requests that grabbed the old pointer can drain first. Tracked on retireWG
// so restore can wait for all corpus handles to close before moving the data
// directory. A nil engine is ignored.
func (i *Instance) retire(old *engine.Engine) {
	if old == nil {
		return
	}
	i.retireWG.Add(1)
	go func() {
		defer i.retireWG.Done()
		time.Sleep(2 * time.Second)
		old.Stop(context.Background())
		_ = old.Reader().Close()
	}()
}

// buildEngine opens current.version's corpus and starts a fresh engine.
func buildEngine(cfg *config.Config, listDir string) (*engine.Engine, error) {
	p := lifecycle.PathsFor(listDir)
	version, err := lifecycle.ReadCurrent(p)
	if err != nil {
		return nil, fmt.Errorf("read current.version: %w", err)
	}
	verDir := filepath.Join(p.VersionsDir, version)

	reader, err := corpus.Open(
		filepath.Join(verDir, corpus.FileCorpusSorted),
		filepath.Join(verDir, corpus.FileCorpusIdx),
		cfg.CorpusReadBufferKB,
	)
	if err != nil {
		return nil, fmt.Errorf("open corpus %s: %w", version, err)
	}

	eng := engine.New(cfg, reader, listDir, verDir)
	startCtx, cancel := context.WithTimeout(context.Background(),
		time.Duration(cfg.PinnedWarmupTimeoutSec+5)*time.Second)
	defer cancel()
	if err := eng.Start(startCtx); err != nil {
		_ = reader.Close()
		return nil, fmt.Errorf("engine start: %w", err)
	}
	slog.Info("corpus live", "version", version, "words", reader.WordCount())
	return eng, nil
}

// ensureCorpus guarantees current.version exists. On a brand-new data
// directory it bootstraps an empty v1 so the server can start with zero
// words and accept the first CSV upload.
func ensureCorpus(cfg *config.Config, listDir string) error {
	p := lifecycle.PathsFor(listDir)
	if _, err := os.Stat(p.CurrentFile); err == nil {
		return nil // already initialised
	}

	slog.Info("no corpus found — creating an empty one (upload a CSV to fill it)",
		"list_dir", listDir)
	if err := os.MkdirAll(p.VersionsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir versions: %w", err)
	}

	seed := filepath.Join(listDir, ".empty.seed.json")
	if err := os.WriteFile(seed, []byte("[]"), 0o644); err != nil {
		return fmt.Errorf("write empty seed: %w", err)
	}
	defer os.Remove(seed)

	outDir := filepath.Join(p.VersionsDir, "v1")
	if _, err := corpus.Bootstrap(corpus.BootstrapOptions{
		SourceJSON:  seed,
		OutDir:      outDir,
		SortChunkMB: cfg.CorpusSortChunkMB,
		SkipStride:  cfg.SkipIndexStride,
		Version:     "v1",
		Mode:        "bootstrap",
	}); err != nil {
		return fmt.Errorf("bootstrap empty v1: %w", err)
	}
	return lifecycle.WriteCurrent(p, "v1")
}
