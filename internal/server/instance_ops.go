package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-suggest-neo/internal/corpus"
	"go-suggest-neo/internal/lifecycle"
)

// IngestResult summarises an /upload.
type IngestResult struct {
	Mode       string `json:"mode"`        // "merge" | "replace"
	ValuesRead int    `json:"values_read"` // non-empty cells pulled from the CSV column
	NewVersion string `json:"new_version"` // version the corpus was promoted to
	WordCount  int64  `json:"word_count"`  // unique words in the live corpus afterwards
}

// IngestCSV pulls one column out of a CSV file, folds it into the corpus
// (deduplicated), and reloads the engine so the new words are queryable
// immediately. mode is "merge" (add to existing, the default) or "replace"
// (the new CSV becomes the whole corpus).
func (i *Instance) IngestCSV(ctx context.Context, csvPath, column, mode string) (*IngestResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	p := i.Paths()

	// Load the blacklist so blacklisted words are never ingested.
	drop, err := loadBlacklistSet(i.listDir)
	if err != nil {
		return nil, fmt.Errorf("load blacklist: %w", err)
	}

	// 1. CSV column → temp JSON array (the format bootstrap ingests).
	tmpJSON := filepath.Join(i.listDir, ".upload.seed.json")
	values, err := csvColumnToJSONArray(csvPath, column, tmpJSON, drop)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpJSON)
	if values == 0 {
		return nil, fmt.Errorf("column %q had no usable (non-empty) values", column)
	}

	// 2. Stage the new data into versions/staging/.
	if _, err := lifecycle.Stage(p, lifecycle.StageOptions{
		SourceJSON:  tmpJSON,
		SortChunkMB: i.cfg.CorpusSortChunkMB,
		SkipStride:  i.cfg.SkipIndexStride,
	}); err != nil {
		return nil, fmt.Errorf("stage CSV: %w", err)
	}

	// 3. Apply. merge = current ∪ staging (dedup). replace = staging only.
	applyMode := lifecycle.ModeMerge
	if mode == "replace" {
		applyMode = lifecycle.ModeReplace
	}
	res, err := lifecycle.Apply(p, lifecycle.ApplyOptions{
		Mode:         applyMode,
		SortChunkMB:  i.cfg.CorpusSortChunkMB,
		SkipStride:   i.cfg.SkipIndexStride,
		MigrateUsage: i.cfg.ApplyMigrateUsage,
	})
	if err != nil {
		_, _ = lifecycle.DeleteStaging(p) // don't leave a half-staged dir behind
		return nil, fmt.Errorf("apply CSV: %w", err)
	}

	// 4. Trim old versions (best-effort; a still-open old version on Windows
	// just gets cleaned on the next upload).
	if _, rerr := lifecycle.Retain(p, i.cfg.CorpusVersionsKept); rerr != nil {
		slog.Debug("retention cleanup deferred", "err", rerr)
	}

	// 5. Swap the live engine to the new version.
	if err := i.reloadLocked(); err != nil {
		return nil, fmt.Errorf("reload after upload: %w", err)
	}

	return &IngestResult{
		Mode:       string(applyMode),
		ValuesRead: values,
		NewVersion: res.NewVersion,
		WordCount:  res.WordCount,
	}, nil
}

// BlacklistResult summarises a /blacklist call.
type BlacklistResult struct {
	Added          int    `json:"added"`           // words newly added to the blacklist
	BlacklistTotal int    `json:"blacklist_total"` // total words now blacklisted
	Reloaded       bool   `json:"reloaded"`        // whether the corpus was rebuilt
	Removed        int64  `json:"removed_from_corpus,omitempty"`
	NewVersion     string `json:"new_version,omitempty"`
	WordCount      int64  `json:"corpus_words,omitempty"`
}

// ApplyBlacklist adds words to the persistent blacklist. If reload is true it
// rebuilds the live corpus without any blacklisted word (a new version) and
// swaps it in. If reload is false it just records the words — they will be
// excluded from the next /upload and applied on the next reload.
func (i *Instance) ApplyBlacklist(ctx context.Context, words []string, reload bool) (*BlacklistResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	added, total, err := addToBlacklist(i.listDir, words)
	if err != nil {
		return nil, fmt.Errorf("save blacklist: %w", err)
	}
	res := &BlacklistResult{Added: added, BlacklistTotal: total}
	if !reload {
		return res, nil
	}

	set, err := loadBlacklistSet(i.listDir)
	if err != nil {
		return nil, err
	}
	built, err := i.rebuildWithoutBlacklist(set)
	if err != nil {
		return nil, fmt.Errorf("rebuild corpus: %w", err)
	}
	if err := i.reloadLocked(); err != nil {
		return nil, fmt.Errorf("reload after blacklist: %w", err)
	}
	res.Reloaded = true
	res.Removed = built.Removed
	res.NewVersion = built.NewVersion
	res.WordCount = built.WordCount
	return res, nil
}

// rebuildWithoutBlacklist streams the current corpus into a fresh version,
// dropping any line in the blacklist set, then flips current.version to it.
// It does NOT reload (the caller does) and never mutates the live version's
// files, so it's safe while the current corpus is open.
func (i *Instance) rebuildWithoutBlacklist(set map[string]struct{}) (*BlacklistResult, error) {
	p := i.Paths()
	cur, err := lifecycle.ReadCurrent(p)
	if err != nil {
		return nil, fmt.Errorf("read current.version: %w", err)
	}
	curDir := filepath.Join(p.VersionsDir, cur)
	nextV, err := lifecycle.NextVersion(p)
	if err != nil {
		return nil, fmt.Errorf("next version: %w", err)
	}
	newDir := filepath.Join(p.VersionsDir, nextV)
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", nextV, err)
	}

	kept, removed, err := filterSortedFile(
		filepath.Join(curDir, corpus.FileCorpusSorted),
		filepath.Join(newDir, corpus.FileCorpusSorted),
		set,
	)
	if err != nil {
		_ = os.RemoveAll(newDir)
		return nil, fmt.Errorf("filter corpus: %w", err)
	}

	idx, err := corpus.BuildSkipIndex(filepath.Join(newDir, corpus.FileCorpusSorted), i.cfg.SkipIndexStride)
	if err != nil {
		_ = os.RemoveAll(newDir)
		return nil, fmt.Errorf("build index: %w", err)
	}
	if err := corpus.WriteSkipIndex(idx, filepath.Join(newDir, corpus.FileCorpusIdx)); err != nil {
		_ = os.RemoveAll(newDir)
		return nil, fmt.Errorf("write index: %w", err)
	}
	man := &corpus.Manifest{
		Version:       nextV,
		CreatedAt:     time.Now().UTC(),
		SourceFile:    "<blacklist filter of " + cur + ">",
		WordCount:     kept,
		ParentVersion: cur,
		Mode:          "blacklist",
	}
	if err := corpus.WriteManifest(man, filepath.Join(newDir, corpus.FileManifest)); err != nil {
		_ = os.RemoveAll(newDir)
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	// Carry usage stats forward (best-effort; stale entries for removed words
	// are harmless).
	_ = copyFileIfExists(
		filepath.Join(curDir, corpus.FileUsageStats),
		filepath.Join(newDir, corpus.FileUsageStats),
	)

	if err := lifecycle.WriteCurrent(p, nextV); err != nil {
		_ = os.RemoveAll(newDir)
		return nil, fmt.Errorf("write current.version: %w", err)
	}
	if _, rerr := lifecycle.Retain(p, i.cfg.CorpusVersionsKept); rerr != nil {
		slog.Debug("retention cleanup deferred", "err", rerr)
	}
	return &BlacklistResult{NewVersion: nextV, Removed: removed, WordCount: kept}, nil
}

// filterSortedFile copies src to dst line by line, skipping any line present
// in drop. Returns (kept, removed) counts. Output is newline-normalised.
func filterSortedFile(src, dst string, drop map[string]struct{}) (kept, removed int64, err error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, 0, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, 0, err
	}
	br := bufio.NewReaderSize(in, 1<<20)
	bw := bufio.NewWriterSize(out, 1<<20)
	for {
		line, rerr := br.ReadString('\n')
		word := strings.TrimRight(line, "\r\n")
		if word != "" {
			if _, bad := drop[word]; bad {
				removed++
			} else {
				if _, werr := bw.WriteString(word); werr != nil {
					out.Close()
					return 0, 0, werr
				}
				if werr := bw.WriteByte('\n'); werr != nil {
					out.Close()
					return 0, 0, werr
				}
				kept++
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return 0, 0, rerr
		}
	}
	if err := bw.Flush(); err != nil {
		out.Close()
		return 0, 0, err
	}
	return kept, removed, out.Close()
}

func copyFileIfExists(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// RestoreFromZip replaces the entire data directory with the contents of a
// backup zip (as produced by /backup) and brings the restored corpus live.
// It is transactional: on any failure the previous data directory is put
// back and the engine is recovered.
func (i *Instance) RestoreFromZip(ctx context.Context, zipPath string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	dataDir := filepath.Clean(i.cfg.DataDir)
	stagingDir := dataDir + ".restore"
	oldDir := dataDir + ".old"

	// Extract into a sibling staging directory.
	_ = os.RemoveAll(stagingDir)
	if err := extractZip(zipPath, stagingDir); err != nil {
		return fmt.Errorf("extract backup: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	// The archive stores paths as "<dataDirName>/<list>/...".
	restoredRoot := filepath.Join(stagingDir, filepath.Base(dataDir))
	curFile := filepath.Join(restoredRoot, i.listName, corpus.FileCurrentVersion)
	if _, err := os.Stat(curFile); err != nil {
		return fmt.Errorf("not a valid backup: missing %s",
			filepath.Join(i.listName, corpus.FileCurrentVersion))
	}

	// Stop the live engine so the OS releases the corpus file handles
	// (required on Windows before we can move the directory).
	if e := i.cur.Swap(nil); e != nil {
		e.Stop(context.Background())
		_ = e.Reader().Close()
	}
	// Wait for any engines still being retired in the background — they hold
	// older corpus files open, which Windows would refuse to move.
	i.retireWG.Wait()

	_ = os.RemoveAll(oldDir)
	if err := renameRetry(dataDir, oldDir); err != nil {
		i.recover()
		return fmt.Errorf("move current data aside: %w", err)
	}
	if err := renameRetry(restoredRoot, dataDir); err != nil {
		_ = os.Rename(oldDir, dataDir) // roll back
		i.recover()
		return fmt.Errorf("install restored data: %w", err)
	}

	neu, err := buildEngine(i.cfg, i.listDir)
	if err != nil {
		_ = os.RemoveAll(dataDir)
		_ = os.Rename(oldDir, dataDir) // roll back
		i.recover()
		return fmt.Errorf("start restored corpus: %w", err)
	}
	i.cur.Store(neu)
	_ = os.RemoveAll(oldDir)
	slog.Info("restore complete", "data_dir", dataDir)
	return nil
}

// recover rebuilds the engine from whatever is on disk after a failed
// restore, so the service keeps serving instead of being left engine-less.
func (i *Instance) recover() {
	if i.cur.Load() != nil {
		return
	}
	if e, err := buildEngine(i.cfg, i.listDir); err == nil {
		i.cur.Store(e)
	} else {
		slog.Error("could not recover engine after failed restore", "err", err)
	}
}
