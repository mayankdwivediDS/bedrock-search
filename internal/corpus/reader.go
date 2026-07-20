package corpus

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Reader is the read-only interface to one corpus version. It holds an
// open *os.File (goroutine-safe thanks to ReadAt) plus the skip index.
//
// The query hot path uses PrefixScan with a small limit; the promotion
// worker uses PrefixScanAll with a per-prefix cap. Both share the same
// seek-then-scan strategy; they differ only in how much they read.
type Reader struct {
	path string
	f    *os.File
	idx  *SkipIndex
	// readBufKB is the bufio buffer size; 0 falls back to 64.
	readBufKB int
}

// Open opens a corpus version for reading. Call Close when done.
func Open(corpusSortedPath, idxPath string, readBufKB int) (*Reader, error) {
	idx, err := LoadSkipIndex(idxPath)
	if err != nil {
		return nil, fmt.Errorf("load skip index: %w", err)
	}
	f, err := os.Open(corpusSortedPath)
	if err != nil {
		return nil, fmt.Errorf("open corpus.sorted: %w", err)
	}
	if readBufKB <= 0 {
		readBufKB = 64
	}
	return &Reader{path: corpusSortedPath, f: f, idx: idx, readBufKB: readBufKB}, nil
}

// Close releases the file handle.
func (r *Reader) Close() error { return r.f.Close() }

// SkipIndex returns the in-memory skip index. Exposed so the promotion
// worker can run its own scans without going through the Reader.
func (r *Reader) SkipIndex() *SkipIndex { return r.idx }

// WordCount returns the total line count recorded at bootstrap time.
func (r *Reader) WordCount() int64 { return r.idx.LineCount }

// PrefixScan returns up to `limit` sorted words that start with `prefix`.
// It reads at most ~`limit` lines from disk (plus a bounded over-read inside
// one block of the sorted file). Safe to call concurrently.
//
// If ctx is already cancelled the function returns early; if ctx is
// cancelled mid-scan it returns whatever it has collected so far. This is
// how the 200 ms request deadline is enforced at the cold path.
func (r *Reader) PrefixScan(ctx context.Context, prefix string, limit int) ([]string, error) {
	if prefix == "" {
		return nil, errors.New("prefix must not be empty")
	}
	if limit <= 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.scan(ctx, prefix, limit)
}

// PrefixScanAll is like PrefixScan but used by the promotion worker: it
// reads *everything* matching prefix up to maxWords. maxWords=0 means no cap
// (only use for small prefixes).
func (r *Reader) PrefixScanAll(ctx context.Context, prefix string, maxWords int) ([]string, error) {
	if prefix == "" {
		return nil, errors.New("prefix must not be empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cap := maxWords
	if cap == 0 {
		cap = 1 << 30 // effectively unbounded
	}
	return r.scan(ctx, prefix, cap)
}

// scan is the shared seek-then-read implementation.
//
// Algorithm:
//  1. Binary-search the skip index for the greatest anchor whose Word <= prefix.
//     That anchor's Offset is our seek target; at worst the match is up to
//     `stride` lines later in the file.
//  2. Seek, wrap in bufio.Scanner, step forward:
//     - skip lines < prefix
//     - collect lines that start with prefix
//     - stop on first line > prefix with no prefix match
//  3. Every N iterations check ctx.Err() to honour the deadline.
func (r *Reader) scan(ctx context.Context, prefix string, limit int) ([]string, error) {
	offset := r.seekOffset(prefix)
	// io.SectionReader uses ReadAt under the hood, which is safe for
	// concurrent calls on a single *os.File — so multiple goroutines can
	// run scans in parallel without a lock.
	sr := io.NewSectionReader(r.f, offset, r.idx.BytesOnDisk-offset)
	sc := bufio.NewScanner(sr)
	// Allow long lines; autocomplete keywords are usually short, but be safe.
	sc.Buffer(make([]byte, r.readBufKB*1024), 1024*1024)

	results := make([]string, 0, limit)
	const ctxCheckEvery = 512
	checked := 0
	for sc.Scan() {
		checked++
		if checked%ctxCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return results, err
			}
		}
		line := sc.Text()
		switch {
		case line < prefix:
			// still in the seek tail before the match region
			continue
		case strings.HasPrefix(line, prefix):
			results = append(results, line)
			if len(results) >= limit {
				return results, nil
			}
		default:
			// past the match region
			return results, nil
		}
	}
	if err := sc.Err(); err != nil {
		return results, err
	}
	return results, nil
}

// seekOffset binary-searches the skip index for the anchor whose Word is the
// greatest <= prefix, returning its Offset. Falls back to 0 if prefix sorts
// before every anchor.
func (r *Reader) seekOffset(prefix string) int64 {
	if len(r.idx.Entries) == 0 {
		return 0
	}
	// sort.Search returns the smallest i such that Entries[i].Word > prefix;
	// we want the anchor just before that.
	i := sort.Search(len(r.idx.Entries), func(i int) bool {
		return r.idx.Entries[i].Word > prefix
	})
	if i == 0 {
		return 0
	}
	return r.idx.Entries[i-1].Offset
}
