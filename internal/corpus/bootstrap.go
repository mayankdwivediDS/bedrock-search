package corpus

import (
	"bufio"
	"container/heap"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go-suggest-neo/internal/normalise"
)

// BootstrapOptions controls the one-time ingestion pipeline that turns a
// caller-supplied JSON array of strings into a version directory holding
// corpus.sorted + corpus.idx + manifest.json.
//
// The source can be huge (40M+ keywords). The pipeline is streaming and
// bounds peak memory to SortChunkMB; at 256 MB default that comfortably
// handles ~10M strings per chunk.
type BootstrapOptions struct {
	SourceJSON    string // path to the input JSON array of strings
	OutDir        string // target version directory (must not exist or be empty)
	SortChunkMB   int    // memory cap per in-memory sort chunk
	SkipStride    int    // write one skip-index entry per N sorted lines
	Version       string // manifest.Version, e.g. "v1"
	ParentVersion string // manifest.ParentVersion ("" for first bootstrap)
	Mode          string // manifest.Mode ("bootstrap" | "replace" | ...)
}

// Bootstrap drives the full pipeline:
//
//	source JSON  ─streaming→ normalise → chunk-sort to temp files
//	             ─k-way merge + dedup→ corpus.sorted
//	             ─single walk────────→ corpus.idx (sparse)
//	             ─write──────────────→ manifest.json
//
// Returns the manifest on success.
func Bootstrap(opts BootstrapOptions) (*Manifest, error) {
	if opts.SortChunkMB <= 0 {
		return nil, errors.New("SortChunkMB must be > 0")
	}
	if opts.SkipStride <= 0 {
		return nil, errors.New("SkipStride must be > 0")
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir out: %w", err)
	}

	tmpDir := filepath.Join(opts.OutDir, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir tmp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	start := time.Now()
	slog.Info("bootstrap: streaming source", "file", opts.SourceJSON)

	// Phase 1: stream-read source JSON, normalise, reject junk, sort chunks to disk.
	chunkPaths, srcCount, rejected, err := streamToSortedChunks(opts.SourceJSON, tmpDir, opts.SortChunkMB)
	if err != nil {
		return nil, fmt.Errorf("stream+sort: %w", err)
	}
	slog.Info("bootstrap: chunks sorted",
		"source_entries", srcCount, "rejected", rejected, "chunks", len(chunkPaths),
		"elapsed", time.Since(start))

	// Phase 2: k-way merge with dedup → corpus.sorted.
	sortedPath := filepath.Join(opts.OutDir, FileCorpusSorted)
	wordCount, err := mergeAndDedup(chunkPaths, sortedPath)
	if err != nil {
		return nil, fmt.Errorf("merge: %w", err)
	}
	slog.Info("bootstrap: merged + deduped",
		"unique_words", wordCount, "elapsed", time.Since(start))

	// Phase 3: build sparse skip index.
	idxPath := filepath.Join(opts.OutDir, FileCorpusIdx)
	idx, err := buildSkipIndex(sortedPath, opts.SkipStride)
	if err != nil {
		return nil, fmt.Errorf("build skip index: %w", err)
	}
	if err := writeSkipIndex(idx, idxPath); err != nil {
		return nil, fmt.Errorf("write skip index: %w", err)
	}
	slog.Info("bootstrap: skip index written",
		"anchors", len(idx.Entries), "stride", idx.Stride,
		"elapsed", time.Since(start))

	// Phase 4: manifest.
	man := &Manifest{
		Version:       opts.Version,
		CreatedAt:     time.Now().UTC(),
		SourceFile:    opts.SourceJSON,
		WordCount:     wordCount,
		ParentVersion: opts.ParentVersion,
		Mode:          opts.Mode,
	}
	if err := writeManifest(man, filepath.Join(opts.OutDir, FileManifest)); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	slog.Info("bootstrap: done",
		"out_dir", opts.OutDir,
		"words", wordCount,
		"skip_anchors", len(idx.Entries),
		"elapsed", time.Since(start))
	return man, nil
}

// streamToSortedChunks reads the source JSON as a stream, normalises each
// string, drops entries that fail the cleanliness policy, and writes sorted
// chunks to tmpDir. Returns the list of chunk file paths, the total number of
// (pre-dedup) input entries, and how many of those were rejected as unclean.
func streamToSortedChunks(srcPath, tmpDir string, chunkMB int) ([]string, int64, int64, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()

	dec := json.NewDecoder(bufio.NewReaderSize(f, 1<<20))
	tok, err := dec.Token()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("read opening token: %w", err)
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '[' {
		return nil, 0, 0, fmt.Errorf("source JSON must be a top-level array, got %v", tok)
	}

	n := normalise.New()
	chunkBytes := int64(chunkMB) * 1024 * 1024
	var (
		paths      []string
		buf        = make([]string, 0, 1<<20)
		bufBytes   int64
		total      int64
		rejected   int64
		chunkIndex int
	)
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		sort.Strings(buf)
		p := filepath.Join(tmpDir, fmt.Sprintf("chunk-%05d.txt", chunkIndex))
		chunkIndex++
		if err := writeLines(p, buf); err != nil {
			return err
		}
		paths = append(paths, p)
		buf = buf[:0]
		bufBytes = 0
		return nil
	}

	for dec.More() {
		var s string
		if err := dec.Decode(&s); err != nil {
			return nil, 0, 0, fmt.Errorf("decode entry %d: %w", total, err)
		}
		total++
		s = n.String(s)
		if !normalise.Accept(s) {
			rejected++
			continue
		}
		buf = append(buf, s)
		bufBytes += int64(len(s)) + 1
		if bufBytes >= chunkBytes {
			if err := flush(); err != nil {
				return nil, 0, 0, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, 0, 0, err
	}
	// Drain closing ']'.
	_, _ = dec.Token()
	return paths, total, rejected, nil
}

func writeLines(path string, lines []string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	for _, s := range lines {
		if _, err := w.WriteString(s); err != nil {
			f.Close()
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// mergeAndDedup performs a k-way merge of sorted chunk files into a single
// sorted, deduplicated output. Memory cost: one line per input chunk.
func mergeAndDedup(chunkPaths []string, outPath string) (int64, error) {
	if len(chunkPaths) == 0 {
		// Still create an empty corpus.sorted so downstream code can open it.
		return 0, writeLines(outPath, nil)
	}

	readers := make([]*bufio.Reader, len(chunkPaths))
	files := make([]*os.File, len(chunkPaths))
	for i, p := range chunkPaths {
		f, err := os.Open(p)
		if err != nil {
			for _, prev := range files[:i] {
				prev.Close()
			}
			return 0, err
		}
		files[i] = f
		readers[i] = bufio.NewReaderSize(f, 1<<20)
	}
	defer func() {
		for _, f := range files {
			if f != nil {
				f.Close()
			}
		}
	}()

	out, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	bw := bufio.NewWriterSize(out, 1<<20)

	h := &mergeHeap{}
	heap.Init(h)

	// Seed the heap with the first line from each chunk.
	for i, r := range readers {
		line, ok, err := readLine(r)
		if err != nil {
			return 0, err
		}
		if ok {
			heap.Push(h, &mergeItem{line: line, src: i})
		}
	}

	var (
		written int64
		prev    string
		havePrev bool
	)
	for h.Len() > 0 {
		item := heap.Pop(h).(*mergeItem)
		if !havePrev || item.line != prev {
			if _, err := bw.WriteString(item.line); err != nil {
				return 0, err
			}
			if err := bw.WriteByte('\n'); err != nil {
				return 0, err
			}
			prev = item.line
			havePrev = true
			written++
		}
		line, ok, err := readLine(readers[item.src])
		if err != nil {
			return 0, err
		}
		if ok {
			heap.Push(h, &mergeItem{line: line, src: item.src})
		}
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return written, nil
}

func readLine(r *bufio.Reader) (string, bool, error) {
	line, err := r.ReadString('\n')
	if err == io.EOF {
		if line == "" {
			return "", false, nil
		}
		return line, true, nil
	}
	if err != nil {
		return "", false, err
	}
	// strip trailing \n (and \r if present)
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, true, nil
}

// mergeHeap is a min-heap of mergeItem. We compare by line; ties broken by
// source index (so reads drain deterministically).
type mergeItem struct {
	line string
	src  int
}
type mergeHeap []*mergeItem

func (h mergeHeap) Len() int { return len(h) }
func (h mergeHeap) Less(i, j int) bool {
	if h[i].line != h[j].line {
		return h[i].line < h[j].line
	}
	return h[i].src < h[j].src
}
func (h mergeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x any)         { *h = append(*h, x.(*mergeItem)) }
func (h *mergeHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// BuildSkipIndex walks a sorted corpus once, recording (word, offset) every
// stride lines (and always the first line, which lets PrefixScan handle
// queries that come before any anchor).
//
// Exported so lifecycle operations (merge, prune) can rebuild the index
// after producing a new sorted file without re-running the full bootstrap.
func BuildSkipIndex(sortedPath string, stride int) (*SkipIndex, error) {
	return buildSkipIndex(sortedPath, stride)
}

func buildSkipIndex(sortedPath string, stride int) (*SkipIndex, error) {
	f, err := os.Open(sortedPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}

	br := bufio.NewReaderSize(f, 1<<20)
	idx := &SkipIndex{Stride: stride, BytesOnDisk: stat.Size()}
	var (
		offset int64
		lines  int64
	)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			if int(lines)%stride == 0 {
				clean := line
				if clean[len(clean)-1] == '\n' {
					clean = clean[:len(clean)-1]
				}
				idx.Entries = append(idx.Entries, SkipEntry{Word: clean, Offset: offset})
			}
			offset += int64(len(line))
			lines++
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	idx.LineCount = lines
	return idx, nil
}

// WriteSkipIndex serialises a SkipIndex to disk. Exported for the same
// reason as BuildSkipIndex.
func WriteSkipIndex(idx *SkipIndex, path string) error { return writeSkipIndex(idx, path) }

// WriteManifest serialises a Manifest to disk as formatted JSON. Exported
// for lifecycle's apply paths that write a post-merge / post-prune manifest.
func WriteManifest(m *Manifest, path string) error { return writeManifest(m, path) }

func writeSkipIndex(idx *SkipIndex, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<16)
	if err := gob.NewEncoder(bw).Encode(idx); err != nil {
		f.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// LoadSkipIndex reads a previously written skip index from disk.
func LoadSkipIndex(path string) (*SkipIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var idx SkipIndex
	if err := gob.NewDecoder(bufio.NewReader(f)).Decode(&idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func writeManifest(m *Manifest, path string) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// LoadManifest reads the per-version manifest.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
