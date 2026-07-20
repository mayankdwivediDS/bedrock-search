package lifecycle

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go-suggest-neo/internal/corpus"
	"go-suggest-neo/internal/usage"
)

// applyPrune produces new corpus = current minus dead prefixes.
//
// "Dead" = a depth-N prefix with <= maxHits lifetime hits in the usage
// tracker. Prefixes that never appear in prefixHits at all are implicitly
// dead (hits == 0). Words under a dead prefix are dropped from the output.
//
// Unlike replace and merge, prune does NOT require a staging directory —
// it operates directly against the current version and produces a new
// version. It does, however, reuse the staging directory as scratch space
// so the commit-atomic flow (write → rename → flip pointer) is identical.
func applyPrune(p Paths, opts ApplyOptions, maxHits uint64) (*ApplyResult, error) {
	prev, _ := ReadCurrent(p)
	if prev == "" {
		return nil, fmt.Errorf("prune requires a current version")
	}
	prevDir := filepath.Join(p.VersionsDir, prev)

	// Load the current version's usage stats to identify live prefixes.
	prevUsage, err := usage.Read(filepath.Join(prevDir, corpus.FileUsageStats))
	if err != nil {
		return nil, fmt.Errorf("read usage: %w", err)
	}
	if prevUsage == nil {
		return nil, fmt.Errorf("no usage stats for %s; cannot determine dead prefixes", prev)
	}

	alive := make(map[string]struct{}, len(prevUsage.PrefixHits))
	for k, v := range prevUsage.PrefixHits {
		if v > maxHits {
			alive[k] = struct{}{}
		}
	}
	if len(alive) == 0 {
		return nil, fmt.Errorf("prune would drop the entire corpus: no prefix has > %d hits", maxHits)
	}

	// Wipe + prepare staging as our scratch workspace.
	if err := os.RemoveAll(p.StagingDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.StagingDir, 0o755); err != nil {
		return nil, err
	}

	// Walk current's corpus.sorted, keep only words whose prefix is alive.
	curPath := filepath.Join(prevDir, corpus.FileCorpusSorted)
	stagePath := filepath.Join(p.StagingDir, corpus.FileCorpusSorted)
	wordCount, dropped, err := pruneFileByPrefix(curPath, stagePath, alive, prevUsage.PrefixDepth)
	if err != nil {
		return nil, fmt.Errorf("prune file: %w", err)
	}

	// Rebuild skip index + manifest.
	idxPath := filepath.Join(p.StagingDir, corpus.FileCorpusIdx)
	idx, err := corpus.BuildSkipIndex(stagePath, opts.SkipStride)
	if err != nil {
		return nil, err
	}
	if err := corpus.WriteSkipIndex(idx, idxPath); err != nil {
		return nil, err
	}
	nextV, err := NextVersion(p)
	if err != nil {
		return nil, err
	}
	man := &corpus.Manifest{
		Version:       nextV,
		CreatedAt:     time.Now().UTC(),
		SourceFile:    fmt.Sprintf("<prune of %s; max_hits=%d>", prev, maxHits),
		WordCount:     wordCount,
		ParentVersion: prev,
		Mode:          string(ModePrune),
	}
	if err := corpus.WriteManifest(man, filepath.Join(p.StagingDir, corpus.FileManifest)); err != nil {
		return nil, err
	}

	// Promote scratch → v(N+1).
	newDir := filepath.Join(p.VersionsDir, nextV)
	if err := os.Rename(p.StagingDir, newDir); err != nil {
		return nil, fmt.Errorf("promote staging: %w", err)
	}

	if opts.MigrateUsage {
		if err := migrateUsage(p, prev, nextV); err != nil {
			_ = err
		}
	}

	if err := WriteCurrent(p, nextV); err != nil {
		return nil, fmt.Errorf("write current.version: %w", err)
	}

	return &ApplyResult{
		NewVersion:    nextV,
		PrevVersion:   prev,
		WordCount:     wordCount,
		Mode:          ModePrune,
		UsageMigrated: opts.MigrateUsage,
		Diff: &DiffResult{
			Dropped:  dropped,
			Retained: wordCount,
			Added:    0,
		},
	}, nil
}

// pruneFileByPrefix walks inPath and copies to outPath only those lines
// whose first `depth` runes are in the alive set. Both files are sorted,
// so we can emit in-order. Returns (kept, dropped).
func pruneFileByPrefix(inPath, outPath string, alive map[string]struct{}, depth int) (int64, int64, error) {
	if depth < 1 {
		depth = 1
	}
	inF, err := os.Open(inPath)
	if err != nil {
		return 0, 0, err
	}
	defer inF.Close()
	outF, err := os.Create(outPath)
	if err != nil {
		return 0, 0, err
	}
	defer outF.Close()
	br := bufio.NewReaderSize(inF, 1<<20)
	bw := bufio.NewWriterSize(outF, 1<<20)

	var kept, dropped int64
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			word := stripNL(line)
			key := truncateRunes(word, depth)
			if _, ok := alive[key]; ok {
				if _, werr := bw.WriteString(word); werr != nil {
					return 0, 0, werr
				}
				if werr := bw.WriteByte('\n'); werr != nil {
					return 0, 0, werr
				}
				kept++
			} else {
				dropped++
			}
		}
		if err != nil {
			break
		}
	}
	if err := bw.Flush(); err != nil {
		return 0, 0, err
	}
	return kept, dropped, nil
}

// truncateRunes returns the first `depth` runes of s. Matches the
// truncation used by usage.Tracker so the alive set lookups line up.
func truncateRunes(s string, depth int) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	if len(runes) <= depth {
		return s
	}
	return string(runes[:depth])
}
