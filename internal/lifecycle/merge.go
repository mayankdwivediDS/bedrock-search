package lifecycle

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go-suggest-neo/internal/corpus"
)

// applyMerge produces new corpus = current ∪ staging (deduplicated) as a
// new version directory. Both inputs are already sorted (bootstrap always
// emits sorted output), so this is a linear-time two-pointer merge.
//
// The staging directory is consumed (renamed into v(N+1)/, then the merged
// file overwrites staging's corpus.sorted inside that dir). This keeps the
// commit-atomic property — current.version is only flipped at the very end.
func applyMerge(p Paths, opts ApplyOptions) (*ApplyResult, error) {
	prev, _ := ReadCurrent(p)
	if prev == "" {
		return nil, fmt.Errorf("merge requires a current version as a baseline")
	}
	nextV, err := NextVersion(p)
	if err != nil {
		return nil, fmt.Errorf("next version: %w", err)
	}

	curPath := filepath.Join(p.VersionsDir, prev, corpus.FileCorpusSorted)
	stagePath := filepath.Join(p.StagingDir, corpus.FileCorpusSorted)
	newDir := filepath.Join(p.VersionsDir, nextV)

	// Write the merged output into a side-file under staging first; after a
	// successful merge we rename staging → v(N+1).
	mergedPath := filepath.Join(p.StagingDir, "corpus.merged")
	wordCount, err := mergeSortedFilesDedup(curPath, stagePath, mergedPath)
	if err != nil {
		return nil, fmt.Errorf("merge sorted files: %w", err)
	}
	// Atomically replace staging's corpus.sorted with the merged output.
	if err := os.Rename(mergedPath, stagePath); err != nil {
		return nil, fmt.Errorf("rename merged: %w", err)
	}

	// Rebuild the skip index over the new corpus.sorted.
	idxPath := filepath.Join(p.StagingDir, corpus.FileCorpusIdx)
	idx, err := corpus.BuildSkipIndex(stagePath, opts.SkipStride)
	if err != nil {
		return nil, fmt.Errorf("build skip index: %w", err)
	}
	if err := corpus.WriteSkipIndex(idx, idxPath); err != nil {
		return nil, fmt.Errorf("write skip index: %w", err)
	}

	// Rewrite the manifest for the new version.
	man := &corpus.Manifest{
		Version:       nextV,
		CreatedAt:     time.Now().UTC(),
		SourceFile:    "<merge of " + prev + " + staging>",
		WordCount:     wordCount,
		ParentVersion: prev,
		Mode:          string(ModeMerge),
	}
	if err := corpus.WriteManifest(man, filepath.Join(p.StagingDir, corpus.FileManifest)); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	// Promote staging → v(N+1).
	if err := os.Rename(p.StagingDir, newDir); err != nil {
		return nil, fmt.Errorf("promote staging: %w", err)
	}

	// Migrate usage: all surviving words (which for merge is the union, so
	// effectively everything from both). prefixHits migrate as-is; surfaced
	// bitmap is filtered to words in the new corpus (the filter is a no-op
	// for merge because v(N+1) ⊇ v(N), but we run it anyway for consistency
	// with the replace path).
	if opts.MigrateUsage {
		if err := migrateUsage(p, prev, nextV); err != nil {
			// non-fatal
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
		Mode:          ModeMerge,
		UsageMigrated: opts.MigrateUsage && prev != "",
	}, nil
}

// mergeSortedFilesDedup streams two sorted inputs and writes their sorted
// union. Duplicates appear once in the output. Returns the line count.
func mergeSortedFilesDedup(aPath, bPath, outPath string) (int64, error) {
	af, err := os.Open(aPath)
	if err != nil {
		return 0, err
	}
	defer af.Close()
	bf, err := os.Open(bPath)
	if err != nil {
		return 0, err
	}
	defer bf.Close()
	of, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer of.Close()

	ar := bufio.NewReaderSize(af, 1<<20)
	br := bufio.NewReaderSize(bf, 1<<20)
	bw := bufio.NewWriterSize(of, 1<<20)

	a, aOK, err := readLine(ar)
	if err != nil {
		return 0, err
	}
	b, bOK, err := readLine(br)
	if err != nil {
		return 0, err
	}

	var written int64
	emit := func(s string) error {
		if _, err := bw.WriteString(s); err != nil {
			return err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return err
		}
		written++
		return nil
	}

	for aOK && bOK {
		switch {
		case a == b:
			if err := emit(a); err != nil {
				return 0, err
			}
			a, aOK, err = readLine(ar)
			if err != nil {
				return 0, err
			}
			b, bOK, err = readLine(br)
			if err != nil {
				return 0, err
			}
		case a < b:
			if err := emit(a); err != nil {
				return 0, err
			}
			a, aOK, err = readLine(ar)
			if err != nil {
				return 0, err
			}
		default:
			if err := emit(b); err != nil {
				return 0, err
			}
			b, bOK, err = readLine(br)
			if err != nil {
				return 0, err
			}
		}
	}
	// Drain tails.
	for aOK {
		if err := emit(a); err != nil {
			return 0, err
		}
		a, aOK, err = readLine(ar)
		if err != nil && err != io.EOF {
			return 0, err
		}
	}
	for bOK {
		if err := emit(b); err != nil {
			return 0, err
		}
		b, bOK, err = readLine(br)
		if err != nil && err != io.EOF {
			return 0, err
		}
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return written, nil
}
