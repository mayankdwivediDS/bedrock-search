package lifecycle

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"go-suggest-neo/internal/corpus"
	"go-suggest-neo/internal/usage"
)

// ApplyMode is a typed enum for the three supported modes.
type ApplyMode string

const (
	ModeReplace ApplyMode = "replace"
	ModeMerge   ApplyMode = "merge"
	ModePrune   ApplyMode = "prune"
)

// ApplyOptions parameterise an apply call.
type ApplyOptions struct {
	Mode         ApplyMode
	SortChunkMB  int    // used by merge/prune when they regenerate a sorted file
	SkipStride   int    // likewise
	MigrateUsage bool   // copy usage stats from old version, filtered to surviving words
	PruneMaxHits uint64 // cutoff for ModePrune; prefixes with <= hits are dropped
}

// ApplyResult is what Apply returns.
type ApplyResult struct {
	NewVersion    string         `json:"new_version"`
	PrevVersion   string         `json:"prev_version"`
	WordCount     int64          `json:"word_count"`
	Mode          ApplyMode      `json:"mode"`
	UsageMigrated bool           `json:"usage_migrated"`
	Diff          *DiffResult    `json:"diff,omitempty"`
}

// Apply promotes a new corpus into a versioned directory, migrates usage
// stats from the previous version (per plan.md §11.4), and atomically
// updates current.version.
//
// Three modes:
//   - ModeReplace: new corpus = staging (requires staging)
//   - ModeMerge:   new corpus = current ∪ staging (requires staging)
//   - ModePrune:   new corpus = current minus dead prefixes (uses usage stats; NO staging required)
func Apply(p Paths, opts ApplyOptions) (*ApplyResult, error) {
	if opts.Mode == "" {
		opts.Mode = ModeReplace
	}
	switch opts.Mode {
	case ModeMerge:
		if !StagingExists(p) {
			return nil, fmt.Errorf("merge requires staging; call Stage first")
		}
		return applyMerge(p, opts)
	case ModePrune:
		return applyPrune(p, opts, opts.PruneMaxHits)
	case ModeReplace:
		if !StagingExists(p) {
			return nil, fmt.Errorf("no staging present; call Stage first")
		}
		// fall through to replace logic below
	default:
		return nil, fmt.Errorf("unknown mode %q", opts.Mode)
	}

	prev, _ := ReadCurrent(p)
	nextV, err := NextVersion(p)
	if err != nil {
		return nil, fmt.Errorf("next version: %w", err)
	}
	newDir := filepath.Join(p.VersionsDir, nextV)

	// Move staging into versions/<nextV>. Rename is atomic on same volume.
	if err := os.Rename(p.StagingDir, newDir); err != nil {
		return nil, fmt.Errorf("promote staging: %w", err)
	}

	// Rewrite the manifest with the real version + mode so rollback /
	// /corpus/versions reports the truth rather than "staging".
	man, err := corpus.LoadManifest(filepath.Join(newDir, corpus.FileManifest))
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	man.Version = nextV
	man.ParentVersion = prev
	man.Mode = string(opts.Mode)
	man.CreatedAt = time.Now().UTC()
	if err := writeManifestFile(newDir, man); err != nil {
		return nil, fmt.Errorf("rewrite manifest: %w", err)
	}

	// Usage migration (plan.md §11.4) — preserve what we knew about surviving
	// words, discard entries for words that were dropped.
	if opts.MigrateUsage && prev != "" {
		if err := migrateUsage(p, prev, nextV); err != nil {
			slog.Warn("usage migration skipped (non-fatal)", "err", err)
		}
	}

	// Atomically flip current.version last — this is the *commit* point.
	if err := WriteCurrent(p, nextV); err != nil {
		// We've already moved staging → vN. If the current.version write
		// fails, we leave the new dir in place — operator can rerun.
		return nil, fmt.Errorf("write current.version (new dir left at %s): %w", newDir, err)
	}

	return &ApplyResult{
		NewVersion:    nextV,
		PrevVersion:   prev,
		WordCount:     man.WordCount,
		Mode:          opts.Mode,
		UsageMigrated: opts.MigrateUsage && prev != "",
	}, nil
}

// migrateUsage loads the previous version's usage.stats.gob, filters the
// surfaced set to words that still exist in the new corpus (two-pointer
// merge), copies prefixHits directly, and writes the result as the new
// version's usage.stats.gob.
//
// prefixHits are version-independent (plain strings), so they migrate as-is.
// The surfaced set is filtered because words removed from the corpus are
// implicitly "no longer surface-able".
func migrateUsage(p Paths, prevVersion, newVersion string) error {
	prevUsagePath := filepath.Join(p.VersionsDir, prevVersion, corpus.FileUsageStats)
	newUsagePath := filepath.Join(p.VersionsDir, newVersion, corpus.FileUsageStats)

	prevSnap, err := usage.Read(prevUsagePath)
	if err != nil {
		return fmt.Errorf("read prev usage: %w", err)
	}
	if prevSnap == nil {
		return nil // nothing to migrate
	}

	newSorted := filepath.Join(p.VersionsDir, newVersion, corpus.FileCorpusSorted)
	survivors, err := filterSurvivors(prevSnap.Surfaced, newSorted)
	if err != nil {
		return fmt.Errorf("filter survivors: %w", err)
	}

	migrated := &usage.Snapshot{
		Version:     prevSnap.Version,
		PrefixHits:  prevSnap.PrefixHits,
		Surfaced:    survivors,
		TotalRecs:   prevSnap.TotalRecs,
		StartedAt:   prevSnap.StartedAt,
		PrefixDepth: prevSnap.PrefixDepth,
	}
	t := usage.New(usage.Config{
		Enabled:         true,
		PrefixDepth:     prevSnap.PrefixDepth,
		SurfacedEnabled: true,
	})
	usage.Restore(t, migrated)
	return usage.Write(t, newUsagePath)
}

// filterSurvivors walks the sorted surfaced slice in parallel with the new
// corpus.sorted (also sorted) and keeps only the words present in the new
// corpus. This is the two-pointer merge from plan.md §11.4.
func filterSurvivors(surfaced []string, newSortedPath string) ([]string, error) {
	sortedSurfaced := append([]string(nil), surfaced...)
	sortSlice(sortedSurfaced)

	f, err := os.Open(newSortedPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)

	var survivors []string
	i := 0
	corpusWord, corpusOK, err := readLine(br)
	if err != nil {
		return nil, err
	}
	for i < len(sortedSurfaced) && corpusOK {
		switch {
		case sortedSurfaced[i] < corpusWord:
			i++ // surfaced word is gone from the corpus — drop it
		case sortedSurfaced[i] > corpusWord:
			corpusWord, corpusOK, err = readLine(br)
		default:
			survivors = append(survivors, sortedSurfaced[i])
			i++
			corpusWord, corpusOK, err = readLine(br)
		}
		if err != nil {
			return nil, err
		}
	}
	return survivors, nil
}
