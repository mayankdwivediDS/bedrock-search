// Package corpus owns the on-disk representation of the immutable word list.
//
// Layout (per list, see plan.md §11.2):
//
//	<data>/<list>/
//	    current.version            (one line: "v7")
//	    versions/
//	        vN/
//	            corpus.sorted      (newline-delimited, UTF-8, sorted, normalised)
//	            corpus.idx         (gob-encoded sparse skip index)
//	            manifest.json      (metadata)
//	            usage.stats.gob    (per-version UsageTracker snapshot)
//	        staging/               (pre-apply workspace)
//
// The *running* server only reads files. Writes happen in three places:
//  1. Bootstrap (this package) when producing a new version.
//  2. Usage snapshotter (internal/usage) writing usage.stats.gob.
//  3. Lifecycle.Apply atomically updating current.version.
package corpus

import "time"

// SkipEntry records one (word, byte-offset) anchor in the sparse skip index.
// Given a sorted corpus, PrefixScan binary-searches this slice to find the
// region that might contain the prefix, then seeks to the offset and scans
// forward line-by-line.
type SkipEntry struct {
	Word   string
	Offset int64
}

// SkipIndex is a slice of anchors sorted by Word ascending, plus bookkeeping
// so readers can sanity-check the file they are reading against.
type SkipIndex struct {
	Entries     []SkipEntry
	Stride      int   // source: config.SkipIndexStride
	LineCount   int64 // total lines in corpus.sorted
	BytesOnDisk int64 // size of corpus.sorted
}

// Manifest is the per-version metadata written at bootstrap / apply time.
type Manifest struct {
	Version       string    `json:"version"`        // e.g. "v7"
	CreatedAt     time.Time `json:"created_at"`
	SourceFile    string    `json:"source_file"`    // original JSON the corpus was built from
	WordCount     int64     `json:"word_count"`
	ParentVersion string    `json:"parent_version"` // "" for the first bootstrap
	Mode          string    `json:"mode"`           // "bootstrap" | "replace" | "merge" | "prune"
}

// File names, relative to a version directory.
const (
	FileCorpusSorted = "corpus.sorted"
	FileCorpusIdx    = "corpus.idx"
	FileManifest     = "manifest.json"
	FileUsageStats   = "usage.stats.gob"
)

// Top-level layout relative to <data>/<list>/.
const (
	FileCurrentVersion = "current.version"
	DirVersions        = "versions"
	DirStaging         = "staging" // inside DirVersions
)
