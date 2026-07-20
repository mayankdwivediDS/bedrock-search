// Package cache implements the HotTrieCache — an LRU of per-prefix tries.
//
// Each entry covers one prefix subtree and holds the words the promotion
// worker materialised. Lookup uses longest-prefix match: a query for
// "marketing" is served by the "mark" entry if present, else "mar", else
// "ma", else a cold miss. This matches plan.md §2.2.
//
// The cache is bounded by a total word count (WORD_CAP, default 100_000,
// shared across all lists). Eviction drops whole entries from the LRU tail
// until the cache is back under cap. Because each entry is its own trie,
// eviction is O(1) — no tree rebuild.
package cache

import (
	"container/list"
	"sync"
	"time"

	"go-suggest-neo/internal/trie"
)

// Entry is one cached prefix subtree.
type Entry struct {
	Prefix   string
	Trie     *trie.Trie
	Size     int       // number of words this trie holds
	LoadedAt time.Time
	hits     uint64
	lruElt   *list.Element
}

// Hits returns the number of lookups served by this entry since it was
// inserted. Exposed for /cache_stats.
func (e *Entry) Hits() uint64 { return e.hits }

// Cache is a shared-pool LRU keyed by prefix string.
type Cache struct {
	mu        sync.Mutex
	entries   map[string]*Entry
	lru       *list.List // front = MRU, back = LRU; values are *Entry
	wordCap   int
	wordCount int
	// Stats.
	lookupsHit    uint64
	lookupsMiss   uint64
	totalEvicted  uint64
	totalInserted uint64
}

// New creates a cache with the given total word cap. wordCap must be > 0.
func New(wordCap int) *Cache {
	return &Cache{
		entries: make(map[string]*Entry),
		lru:     list.New(),
		wordCap: wordCap,
	}
}

// Insert adds a fresh entry. If prefix is already present, the old entry is
// replaced and its words are deducted before the new words are added.
// Returns the prefixes evicted (if any) so the caller can update external
// state like the ledger's promoted flag.
func (c *Cache) Insert(prefix string, t *trie.Trie, size int) (evicted []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.totalInserted++

	if existing, ok := c.entries[prefix]; ok {
		c.wordCount -= existing.Size
		c.lru.Remove(existing.lruElt)
		delete(c.entries, prefix)
	}

	e := &Entry{Prefix: prefix, Trie: t, Size: size, LoadedAt: time.Now()}
	e.lruElt = c.lru.PushFront(e)
	c.entries[prefix] = e
	c.wordCount += size

	// Evict from the tail until under cap.
	for c.wordCount > c.wordCap && c.lru.Len() > 1 {
		tail := c.lru.Back()
		if tail == nil {
			break
		}
		victim := tail.Value.(*Entry)
		if victim.Prefix == prefix {
			// Never evict the entry we just inserted; if it alone exceeds
			// cap that's the caller's problem (they set PROMOTION_MAX_WORDS
			// wrong — validated at boot but belt-and-suspenders here).
			break
		}
		c.lru.Remove(tail)
		delete(c.entries, victim.Prefix)
		c.wordCount -= victim.Size
		c.totalEvicted++
		evicted = append(evicted, victim.Prefix)
	}
	return evicted
}

// Lookup returns the entry whose Prefix is the longest prefix of query,
// or nil. A hit promotes the entry to MRU.
//
// The scan is O(len(query)) map lookups — each call tries query[:n],
// query[:n-1], ... query[:1] and returns the first hit. For short queries
// (autocomplete is usually <20 chars) this is effectively O(1).
func (c *Cache) Lookup(query string) (*Entry, bool) {
	if query == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(query); i >= 1; i-- {
		if e, ok := c.entries[query[:i]]; ok {
			c.lru.MoveToFront(e.lruElt)
			e.hits++
			c.lookupsHit++
			return e, true
		}
	}
	c.lookupsMiss++
	return nil, false
}

// Has reports whether a specific prefix is resident. Unlike Lookup it does
// not perform longest-prefix matching and does not mutate LRU order.
func (c *Cache) Has(prefix string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[prefix]
	return ok
}

// Evict removes a specific prefix if present. Returns true if it was there.
func (c *Cache) Evict(prefix string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[prefix]
	if !ok {
		return false
	}
	c.lru.Remove(e.lruElt)
	delete(c.entries, prefix)
	c.wordCount -= e.Size
	c.totalEvicted++
	return true
}

// WordCount returns the total number of words across all resident entries.
func (c *Cache) WordCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wordCount
}

// Len returns the number of resident entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

// Stats is a cheap observability bundle. HitRate is computed on the fly.
type Stats struct {
	Entries       int
	WordCount     int
	WordCap       int
	LookupsHit    uint64
	LookupsMiss   uint64
	HitRate       float64
	TotalInserted uint64
	TotalEvicted  uint64
}

// Stats returns a snapshot of counters.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := c.lookupsHit + c.lookupsMiss
	var rate float64
	if total > 0 {
		rate = float64(c.lookupsHit) / float64(total)
	}
	return Stats{
		Entries:       c.lru.Len(),
		WordCount:     c.wordCount,
		WordCap:       c.wordCap,
		LookupsHit:    c.lookupsHit,
		LookupsMiss:   c.lookupsMiss,
		HitRate:       rate,
		TotalInserted: c.totalInserted,
		TotalEvicted:  c.totalEvicted,
	}
}

// Prefixes returns the resident prefixes ordered MRU → LRU. Useful for
// debugging + the /cache_stats endpoint.
func (c *Cache) Prefixes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, c.lru.Len())
	for e := c.lru.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(*Entry).Prefix)
	}
	return out
}
