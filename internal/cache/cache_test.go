package cache

import (
	"testing"

	"go-suggest-neo/internal/trie"
)

// tinyTrie builds a trie containing the given words.
func tinyTrie(words ...string) *trie.Trie {
	t := trie.New().WithoutOriginalDict()
	t.Insert(words...)
	return t
}

func TestLookup_LongestPrefixMatch(t *testing.T) {
	c := New(1000)
	c.Insert("ma", tinyTrie("macadamia", "madrid"), 2)
	c.Insert("mark", tinyTrie("market", "marketing", "markdown"), 3)

	e, ok := c.Lookup("marketing")
	if !ok {
		t.Fatal("expected hit")
	}
	if e.Prefix != "mark" {
		t.Errorf("longest-prefix match wrong: got %q, want %q", e.Prefix, "mark")
	}

	// A shorter query falls back to the shorter prefix entry.
	e2, ok := c.Lookup("madrid")
	if !ok || e2.Prefix != "ma" {
		t.Errorf("short query: got %v/%v, want ma", e2, ok)
	}

	// No matching prefix at all.
	if _, ok := c.Lookup("xylophone"); ok {
		t.Error("unexpected hit on cold prefix")
	}
}

func TestLookup_PromotesToMRU(t *testing.T) {
	c := New(1000)
	c.Insert("aa", tinyTrie("aa1"), 1)
	c.Insert("bb", tinyTrie("bb1"), 1)
	c.Insert("cc", tinyTrie("cc1"), 1)
	// Order now: cc (MRU), bb, aa (LRU).
	c.Lookup("aaa") // hit on aa → moves to MRU
	order := c.Prefixes()
	if len(order) != 3 || order[0] != "aa" {
		t.Errorf("MRU after Lookup(aaa) = %v, want aa at front", order)
	}
}

func TestInsert_EvictsLRUWhenOverCap(t *testing.T) {
	c := New(5) // tiny cap
	c.Insert("a", tinyTrie("apple"), 2)
	c.Insert("b", tinyTrie("bravo"), 2)
	// wordCount = 4, within cap.
	evicted := c.Insert("c", tinyTrie("candy", "carrot"), 2)
	// wordCount = 6, over cap → evict LRU = "a".
	if len(evicted) != 1 || evicted[0] != "a" {
		t.Errorf("evicted = %v, want [a]", evicted)
	}
	if c.Has("a") {
		t.Error("evicted entry still present")
	}
	if c.WordCount() != 4 {
		t.Errorf("wordCount after eviction = %d, want 4", c.WordCount())
	}
}

func TestInsert_ReplaceExistingPrefix(t *testing.T) {
	c := New(100)
	c.Insert("mark", tinyTrie("market"), 1)
	if c.WordCount() != 1 {
		t.Fatalf("wordCount = %d, want 1", c.WordCount())
	}
	c.Insert("mark", tinyTrie("market", "marketing", "markdown"), 3)
	if c.WordCount() != 3 {
		t.Fatalf("wordCount after replace = %d, want 3", c.WordCount())
	}
	if c.Len() != 1 {
		t.Fatalf("entries after replace = %d, want 1", c.Len())
	}
}

func TestEvict_RemovesEntry(t *testing.T) {
	c := New(100)
	c.Insert("x", tinyTrie("xray"), 1)
	if !c.Evict("x") {
		t.Fatal("Evict returned false on existing entry")
	}
	if c.Has("x") {
		t.Error("entry still present after Evict")
	}
	if c.Evict("x") {
		t.Error("Evict returned true on missing entry")
	}
}

func TestStats_TracksHitsAndMisses(t *testing.T) {
	c := New(100)
	c.Insert("a", tinyTrie("apple"), 1)
	c.Lookup("apple") // hit via prefix "a"
	c.Lookup("apple") // hit
	c.Lookup("zzz")   // miss

	s := c.Stats()
	if s.LookupsHit != 2 {
		t.Errorf("hits = %d, want 2", s.LookupsHit)
	}
	if s.LookupsMiss != 1 {
		t.Errorf("misses = %d, want 1", s.LookupsMiss)
	}
	// 2/3 ≈ 0.666…
	if s.HitRate < 0.6 || s.HitRate > 0.7 {
		t.Errorf("hit rate = %f, want ~0.667", s.HitRate)
	}
}

func TestLookup_EmptyQueryReturnsMiss(t *testing.T) {
	c := New(100)
	c.Insert("a", tinyTrie("apple"), 1)
	if _, ok := c.Lookup(""); ok {
		t.Error("empty query should not match")
	}
}
