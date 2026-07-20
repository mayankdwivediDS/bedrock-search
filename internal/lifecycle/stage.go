package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"

	"go-suggest-neo/internal/corpus"
)

// StageOptions describes the source and sort params for a stage operation.
type StageOptions struct {
	SourceJSON  string
	SortChunkMB int
	SkipStride  int
}

// StageResult is what Stage returns to callers.
type StageResult struct {
	StagingDir string
	WordCount  int64
}

// Stage runs the bootstrap pipeline against a caller-supplied JSON into
// the staging workspace. If the staging dir already exists it is wiped
// first — staging is meant to be overwritten on each fresh invocation.
// Returns the count of deduplicated words in the staged corpus.
func Stage(p Paths, opts StageOptions) (*StageResult, error) {
	if opts.SourceJSON == "" {
		return nil, fmt.Errorf("SourceJSON is required")
	}
	if _, err := os.Stat(opts.SourceJSON); err != nil {
		return nil, fmt.Errorf("source not readable: %w", err)
	}

	// Wipe any prior staging attempt.
	if err := os.RemoveAll(p.StagingDir); err != nil {
		return nil, fmt.Errorf("wipe staging: %w", err)
	}
	if err := os.MkdirAll(p.StagingDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir staging: %w", err)
	}

	// Mark the manifest version as "staging" so nothing can mistake it
	// for an applied version. The real version tag is assigned in Apply.
	parent, _ := ReadCurrent(p)
	man, err := corpus.Bootstrap(corpus.BootstrapOptions{
		SourceJSON:    opts.SourceJSON,
		OutDir:        p.StagingDir,
		SortChunkMB:   opts.SortChunkMB,
		SkipStride:    opts.SkipStride,
		Version:       "staging",
		ParentVersion: parent,
		Mode:          "stage",
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	return &StageResult{
		StagingDir: p.StagingDir,
		WordCount:  man.WordCount,
	}, nil
}

// StagingExists reports whether a staging directory is currently present.
func StagingExists(p Paths) bool {
	_, err := os.Stat(filepath.Join(p.StagingDir, corpus.FileCorpusSorted))
	return err == nil
}
