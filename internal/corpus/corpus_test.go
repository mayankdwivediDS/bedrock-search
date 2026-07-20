package corpus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// writeSourceJSON writes a JSON array of strings to a temp file and returns
// its path.
func writeSourceJSON(t *testing.T, words []string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "src.json")
	raw, err := json.Marshal(words)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBootstrapAndReader_BasicSortAndDedup(t *testing.T) {
	src := writeSourceJSON(t, []string{
		"Banana", "apple", "apple", "café", "Cafe", "durian", "  ", "",
		"apricot", "blueberry", "Blueberry", "avocado",
	})
	outDir := filepath.Join(t.TempDir(), "v1")

	man, err := Bootstrap(BootstrapOptions{
		SourceJSON:  src,
		OutDir:      outDir,
		SortChunkMB: 1, // force tiny chunks to exercise the merge path
		SkipStride:  2, // very short stride to exercise binary search
		Version:     "v1",
		Mode:        "bootstrap",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// After normalise + dedup we expect: apple, apricot, avocado, banana,
	// blueberry, cafe, durian. ("café" normalises to "cafe", which also
	// matches the explicit "Cafe" entry after lowercase.)
	want := []string{"apple", "apricot", "avocado", "banana", "blueberry", "cafe", "durian"}

	if man.WordCount != int64(len(want)) {
		t.Errorf("manifest word_count = %d, want %d", man.WordCount, len(want))
	}

	r, err := Open(
		filepath.Join(outDir, FileCorpusSorted),
		filepath.Join(outDir, FileCorpusIdx),
		64,
	)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer r.Close()

	got, err := r.PrefixScan(context.Background(), "a", 100)
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	wantA := []string{"apple", "apricot", "avocado"}
	if !equalStrings(got, wantA) {
		t.Errorf("prefix 'a': got %v, want %v", got, wantA)
	}

	got, err = r.PrefixScan(context.Background(), "b", 100)
	if err != nil {
		t.Fatalf("PrefixScan b: %v", err)
	}
	wantB := []string{"banana", "blueberry"}
	if !equalStrings(got, wantB) {
		t.Errorf("prefix 'b': got %v, want %v", got, wantB)
	}
}

func TestPrefixScan_HonoursLimit(t *testing.T) {
	words := make([]string, 500)
	for i := range words {
		words[i] = fmtWord(i)
	}
	src := writeSourceJSON(t, words)
	outDir := filepath.Join(t.TempDir(), "v1")
	if _, err := Bootstrap(BootstrapOptions{
		SourceJSON:  src, OutDir: outDir,
		SortChunkMB: 1, SkipStride: 16, Version: "v1", Mode: "bootstrap",
	}); err != nil {
		t.Fatal(err)
	}

	r, err := Open(
		filepath.Join(outDir, FileCorpusSorted),
		filepath.Join(outDir, FileCorpusIdx),
		64,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, err := r.PrefixScan(context.Background(), "w", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Errorf("limit=10: got %d results", len(got))
	}
	// All returned words must start with the prefix.
	for _, w := range got {
		if len(w) < 1 || w[0] != 'w' {
			t.Errorf("unexpected result %q for prefix 'w'", w)
		}
	}
}

func TestPrefixScan_EmptyMiss(t *testing.T) {
	src := writeSourceJSON(t, []string{"alpha", "bravo"})
	outDir := filepath.Join(t.TempDir(), "v1")
	if _, err := Bootstrap(BootstrapOptions{
		SourceJSON:  src, OutDir: outDir,
		SortChunkMB: 1, SkipStride: 2, Version: "v1", Mode: "bootstrap",
	}); err != nil {
		t.Fatal(err)
	}
	r, err := Open(
		filepath.Join(outDir, FileCorpusSorted),
		filepath.Join(outDir, FileCorpusIdx),
		64,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, err := r.PrefixScan(context.Background(), "zzz", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no results, got %v", got)
	}
}

func TestPrefixScan_CancelledContext(t *testing.T) {
	src := writeSourceJSON(t, []string{"alpha", "bravo", "charlie"})
	outDir := filepath.Join(t.TempDir(), "v1")
	if _, err := Bootstrap(BootstrapOptions{
		SourceJSON:  src, OutDir: outDir,
		SortChunkMB: 1, SkipStride: 1, Version: "v1", Mode: "bootstrap",
	}); err != nil {
		t.Fatal(err)
	}
	r, err := Open(
		filepath.Join(outDir, FileCorpusSorted),
		filepath.Join(outDir, FileCorpusIdx),
		64,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = r.PrefixScan(ctx, "a", 10)
	if err == nil {
		t.Error("expected ctx.Err() back, got nil")
	}
}

func TestManifestRoundTrip(t *testing.T) {
	src := writeSourceJSON(t, []string{"apple"})
	outDir := filepath.Join(t.TempDir(), "v1")
	_, err := Bootstrap(BootstrapOptions{
		SourceJSON:    src,
		OutDir:        outDir,
		SortChunkMB:   1,
		SkipStride:    1,
		Version:       "v1",
		ParentVersion: "",
		Mode:          "bootstrap",
	})
	if err != nil {
		t.Fatal(err)
	}
	man, err := LoadManifest(filepath.Join(outDir, FileManifest))
	if err != nil {
		t.Fatal(err)
	}
	if man.Version != "v1" || man.Mode != "bootstrap" || man.WordCount != 1 {
		t.Errorf("unexpected manifest: %+v", man)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

// fmtWord maps 0..999 to "aaa".. by mixing lowercase letters so we get a
// diverse distribution across all first characters for limit tests.
func fmtWord(i int) string {
	letters := []byte("abcdefghijklmnopqrstuvwxyz")
	a := letters[i%26]
	b := letters[(i/26)%26]
	c := letters[(i/(26*26))%26]
	return string([]byte{a, b, c})
}
