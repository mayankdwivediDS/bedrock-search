package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-suggest-neo/internal/corpus"
	"go-suggest-neo/internal/usage"
)

// seed runs an initial bootstrap into versions/v1/ so subsequent tests
// have something to stage over.
func seed(t *testing.T, words []string) (Paths, string) {
	t.Helper()
	listDir := t.TempDir()
	p := PathsFor(listDir)
	if err := os.MkdirAll(p.VersionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(t.TempDir(), "src.json")
	raw, _ := json.Marshal(words)
	if err := os.WriteFile(src, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(p.VersionsDir, "v1")
	if _, err := corpus.Bootstrap(corpus.BootstrapOptions{
		SourceJSON:  src,
		OutDir:      outDir,
		SortChunkMB: 1,
		SkipStride:  8,
		Version:     "v1",
		Mode:        "bootstrap",
	}); err != nil {
		t.Fatal(err)
	}
	if err := WriteCurrent(p, "v1"); err != nil {
		t.Fatal(err)
	}
	return p, src
}

func writeJSON(t *testing.T, words []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.json")
	raw, _ := json.Marshal(words)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStageAndDiff(t *testing.T) {
	p, _ := seed(t, []string{"apple", "apricot", "banana", "cherry"})
	newSrc := writeJSON(t, []string{"apple", "apricot", "blueberry", "date"})

	if _, err := Stage(p, StageOptions{SourceJSON: newSrc, SortChunkMB: 1, SkipStride: 4}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if !StagingExists(p) {
		t.Fatal("staging should exist after Stage")
	}
	d, err := Diff(p, "v1")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	// Retained: apple, apricot (2). Dropped: banana, cherry (2). Added: blueberry, date (2).
	if d.Retained != 2 || d.Dropped != 2 || d.Added != 2 {
		t.Errorf("diff = %+v, want retained=2 dropped=2 added=2", d)
	}
}

func TestApplyReplace_BumpsVersionAndMigratesUsage(t *testing.T) {
	p, _ := seed(t, []string{"apple", "apricot", "banana"})

	// Pre-populate v1's usage stats: pretend "apple" and "banana" have been
	// surfaced.
	v1Dir := filepath.Join(p.VersionsDir, "v1")
	tr := usage.New(usage.Config{Enabled: true, PrefixDepth: 3, SurfacedEnabled: true})
	tr.Record("app", []string{"apple"})
	tr.Record("ban", []string{"banana"})
	tr.Record("app", []string{"apple"}) // build up prefixHits
	if err := usage.Write(tr, filepath.Join(v1Dir, corpus.FileUsageStats)); err != nil {
		t.Fatal(err)
	}

	// Stage a new corpus that drops "banana" and adds "blueberry".
	newSrc := writeJSON(t, []string{"apple", "apricot", "blueberry"})
	if _, err := Stage(p, StageOptions{SourceJSON: newSrc, SortChunkMB: 1, SkipStride: 4}); err != nil {
		t.Fatal(err)
	}

	res, err := Apply(p, ApplyOptions{Mode: ModeReplace, MigrateUsage: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.NewVersion != "v2" {
		t.Errorf("NewVersion = %q, want v2", res.NewVersion)
	}
	if res.PrevVersion != "v1" {
		t.Errorf("PrevVersion = %q, want v1", res.PrevVersion)
	}
	cur, _ := ReadCurrent(p)
	if cur != "v2" {
		t.Errorf("current.version = %q, want v2", cur)
	}

	// Verify usage migration: "apple" (survived) kept surfaced, "banana"
	// (dropped from corpus) removed from surfaced set.
	v2Usage, err := usage.Read(filepath.Join(p.VersionsDir, "v2", corpus.FileUsageStats))
	if err != nil {
		t.Fatalf("read v2 usage: %v", err)
	}
	if v2Usage == nil {
		t.Fatal("expected migrated usage to be written")
	}
	surfaced := map[string]bool{}
	for _, w := range v2Usage.Surfaced {
		surfaced[w] = true
	}
	if !surfaced["apple"] {
		t.Error("apple should be retained in migrated surfaced set")
	}
	if surfaced["banana"] {
		t.Error("banana should be removed from migrated surfaced set (word dropped)")
	}
	// prefixHits migrate as-is.
	if v2Usage.PrefixHits["app"] != 2 || v2Usage.PrefixHits["ban"] != 1 {
		t.Errorf("prefixHits not preserved: %v", v2Usage.PrefixHits)
	}
}

func TestRollback_FlipsCurrentVersion(t *testing.T) {
	p, _ := seed(t, []string{"one", "two", "three"})
	newSrc := writeJSON(t, []string{"one", "two", "three", "four"})
	if _, err := Stage(p, StageOptions{SourceJSON: newSrc, SortChunkMB: 1, SkipStride: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(p, ApplyOptions{Mode: ModeReplace}); err != nil {
		t.Fatal(err)
	}

	prev, err := Rollback(p, "v1")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if prev != "v2" {
		t.Errorf("prev = %q, want v2", prev)
	}
	cur, _ := ReadCurrent(p)
	if cur != "v1" {
		t.Errorf("current.version = %q, want v1 after rollback", cur)
	}
}

func TestRollback_RejectsMissingVersion(t *testing.T) {
	p, _ := seed(t, []string{"a"})
	if _, err := Rollback(p, "v99"); err == nil {
		t.Error("expected error for missing version")
	}
}

func TestApplyMerge_UnionsCurrentAndStaging(t *testing.T) {
	p, _ := seed(t, []string{"apple", "banana", "cherry"})
	// Stage a set that overlaps (apple) and adds (blueberry, date).
	newSrc := writeJSON(t, []string{"apple", "blueberry", "date"})
	if _, err := Stage(p, StageOptions{SourceJSON: newSrc, SortChunkMB: 1, SkipStride: 4}); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(p, ApplyOptions{Mode: ModeMerge, SkipStride: 4})
	if err != nil {
		t.Fatalf("apply merge: %v", err)
	}
	if res.NewVersion != "v2" || res.Mode != ModeMerge {
		t.Errorf("unexpected result: %+v", res)
	}
	// Union should be {apple, banana, blueberry, cherry, date} = 5.
	if res.WordCount != 5 {
		t.Errorf("merged word count = %d, want 5", res.WordCount)
	}
	cur, _ := ReadCurrent(p)
	if cur != "v2" {
		t.Errorf("current.version = %q, want v2", cur)
	}
}

func TestApplyPrune_DropsWordsUnderDeadPrefixes(t *testing.T) {
	p, _ := seed(t, []string{
		"apple", "apricot",
		"banana", "blueberry",
		"cherry", "coconut",
	})

	// Manually write usage stats that mark only "ap*" as alive.
	v1Dir := filepath.Join(p.VersionsDir, "v1")
	tr := usage.New(usage.Config{Enabled: true, PrefixDepth: 2, SurfacedEnabled: true})
	tr.Record("ap", []string{"apple", "apricot"})
	tr.Record("ap", []string{"apple"})
	// No records for "ba", "ch" — implicitly dead.
	if err := usage.Write(tr, filepath.Join(v1Dir, corpus.FileUsageStats)); err != nil {
		t.Fatal(err)
	}

	res, err := Apply(p, ApplyOptions{
		Mode:         ModePrune,
		SkipStride:   4,
		PruneMaxHits: 0, // keep only prefixes with > 0 hits
	})
	if err != nil {
		t.Fatalf("apply prune: %v", err)
	}
	if res.WordCount != 2 {
		t.Errorf("pruned word count = %d, want 2 (apple, apricot)", res.WordCount)
	}
	if res.Diff == nil || res.Diff.Dropped != 4 {
		t.Errorf("dropped count = %+v, want 4 (banana, blueberry, cherry, coconut)", res.Diff)
	}
	cur, _ := ReadCurrent(p)
	if cur != "v2" {
		t.Errorf("current.version = %q, want v2", cur)
	}

	// Verify the actual corpus.sorted on disk matches.
	body, err := os.ReadFile(filepath.Join(p.VersionsDir, "v2", corpus.FileCorpusSorted))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if got != "apple\napricot\n" {
		t.Errorf("pruned corpus content = %q, want \"apple\\napricot\\n\"", got)
	}
}

func TestApplyPrune_RejectsDropEntireCorpus(t *testing.T) {
	p, _ := seed(t, []string{"apple", "banana"})
	// No usage data → every prefix is "dead" → pruning would nuke everything.
	v1Dir := filepath.Join(p.VersionsDir, "v1")
	tr := usage.New(usage.Config{Enabled: true, PrefixDepth: 2, SurfacedEnabled: true})
	if err := usage.Write(tr, filepath.Join(v1Dir, corpus.FileUsageStats)); err != nil {
		t.Fatal(err)
	}

	_, err := Apply(p, ApplyOptions{Mode: ModePrune, SkipStride: 4, PruneMaxHits: 0})
	if err == nil {
		t.Error("expected prune to refuse dropping the entire corpus")
	}
}

func TestDeleteVersion_DeletesRetainedVersion(t *testing.T) {
	p, _ := seed(t, []string{"apple"})
	// Apply v2 so v1 is retained but not current.
	newSrc := writeJSON(t, []string{"apple", "banana"})
	if _, err := Stage(p, StageOptions{SourceJSON: newSrc, SortChunkMB: 1, SkipStride: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(p, ApplyOptions{Mode: ModeReplace}); err != nil {
		t.Fatal(err)
	}

	removed, err := DeleteVersion(p, "v1")
	if err != nil {
		t.Fatalf("delete v1: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(removed), "versions/v1") {
		t.Errorf("removed path = %q, expected to end in versions/v1", removed)
	}
	if _, statErr := os.Stat(filepath.Join(p.VersionsDir, "v1")); !os.IsNotExist(statErr) {
		t.Error("v1 directory should be gone")
	}
}

func TestDeleteVersion_RejectsCurrentVersion(t *testing.T) {
	p, _ := seed(t, []string{"apple"})
	_, err := DeleteVersion(p, "v1")
	if err == nil {
		t.Error("expected error deleting current version")
	}
	if !strings.Contains(err.Error(), "current") {
		t.Errorf("error should mention 'current', got: %v", err)
	}
}

func TestDeleteVersion_RejectsMissing(t *testing.T) {
	p, _ := seed(t, []string{"apple"})
	_, err := DeleteVersion(p, "v99")
	if err == nil {
		t.Error("expected error for missing version")
	}
}

func TestDeleteVersion_RejectsInvalidNames(t *testing.T) {
	p, _ := seed(t, []string{"apple"})
	bad := []string{"", "staging", "../evil", `v1/sub`, `v1\win`, "v1.bak"}
	for _, n := range bad {
		if _, err := DeleteVersion(p, n); err == nil {
			t.Errorf("expected error for invalid name %q, got nil", n)
		}
	}
}

func TestDeleteStaging_RemovesWorkspace(t *testing.T) {
	p, _ := seed(t, []string{"apple"})
	if _, err := Stage(p, StageOptions{
		SourceJSON: writeJSON(t, []string{"apple", "banana"}),
		SortChunkMB: 1, SkipStride: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if !StagingExists(p) {
		t.Fatal("staging should exist after Stage")
	}
	if _, err := DeleteStaging(p); err != nil {
		t.Fatalf("delete staging: %v", err)
	}
	if StagingExists(p) {
		t.Error("staging should be gone after DeleteStaging")
	}
}

func TestDeleteStaging_RejectsMissing(t *testing.T) {
	p, _ := seed(t, []string{"apple"})
	if _, err := DeleteStaging(p); err == nil {
		t.Error("expected error when no staging present")
	}
}

func TestRetain_DeletesOlderVersions(t *testing.T) {
	p, _ := seed(t, []string{"a"})
	// Build v2, v3, v4.
	for _, body := range [][]string{{"a", "b"}, {"a", "b", "c"}, {"a", "b", "c", "d"}} {
		src := writeJSON(t, body)
		if _, err := Stage(p, StageOptions{SourceJSON: src, SortChunkMB: 1, SkipStride: 4}); err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(p, ApplyOptions{Mode: ModeReplace}); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := Retain(p, 2)
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	// We built v1..v4; keep=2 → delete v1 and v2.
	if len(deleted) != 2 {
		t.Fatalf("deleted %v, want 2", deleted)
	}
	names, _ := ListVersions(p)
	if len(names) != 2 {
		t.Errorf("versions remaining: %v", names)
	}
}
