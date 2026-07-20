package usage

import (
	"fmt"
	"sync"
	"testing"
)

// TestRecord_ConcurrentSameKeyStaysUnique tries to reproduce the bug where
// /usage/hot returned multiple entries with byte-identical prefix strings
// (["abo", 207], ["abo", 2], ["abo", 1], ["abo", 1]) after a batch of
// concurrent queries to the running server.
//
// If the tracker is correct, 100 goroutines each calling Record("about", …)
// must produce exactly one map key ("abo" at depth 3) with hits == 100×N.
func TestRecord_ConcurrentSameKeyStaysUnique(t *testing.T) {
	tr := New(Config{Enabled: true, PrefixDepth: 3, SurfacedEnabled: true})

	const goroutines = 100
	const perGoroutine = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				tr.Record("about", []string{"about"})
			}
		}()
	}
	wg.Wait()

	hot := tr.Hot(10)
	aboCount := 0
	for _, p := range hot {
		if p.Prefix == "abo" {
			aboCount++
		}
	}
	if aboCount != 1 {
		t.Errorf("expected exactly 1 map key for 'abo', got %d", aboCount)
		for _, p := range hot {
			t.Logf("  key=%q bytes=%x hits=%d", p.Prefix, []byte(p.Prefix), p.Hits)
		}
	}

	// Also sanity: total hits should be goroutines*perGoroutine.
	expected := uint64(goroutines * perGoroutine)
	var got uint64
	for _, p := range hot {
		if p.Prefix == "abo" {
			got += p.Hits
		}
	}
	if got != expected {
		t.Errorf("total hits for 'abo' = %d, want %d", got, expected)
	}
}

// TestRecord_MixedShortAndLongPrefixes exercises the truncatePrefix boundary
// between "short-enough, returns original s" and "longer, returns new string".
// Go map semantics say both paths must collapse to one entry for "abo".
func TestRecord_MixedShortAndLongPrefixes(t *testing.T) {
	tr := New(Config{Enabled: true, PrefixDepth: 3, SurfacedEnabled: false})

	// "abo" → short path (returns s as-is).
	// "abou" / "about" / "aboutus" → long path (returns string(runes[:3])).
	queries := []string{"abo", "abou", "about", "aboutus"}
	for i := 0; i < 1000; i++ {
		for _, q := range queries {
			tr.Record(q, nil)
		}
	}

	hot := tr.Hot(10)
	if len(hot) != 1 {
		t.Errorf("expected 1 map key, got %d", len(hot))
		for _, p := range hot {
			t.Logf("  key=%q bytes=%x hits=%d", p.Prefix, []byte(p.Prefix), p.Hits)
		}
	}
	if hot[0].Hits != 4000 {
		t.Errorf("expected 4000 hits, got %d", hot[0].Hits)
	}
}

// TestAccessors_AgreeOnUniqueKeyCount guards against a regression where
// Hot/Cold/Stats/DumpPrefixHits returned inconsistent shapes because some
// preserved every iteration yield while others collapsed them. After the
// defensive dedup in dedupedEntriesLocked, all four accessors must report
// the same set of logical keys with the same total hit count.
func TestAccessors_AgreeOnUniqueKeyCount(t *testing.T) {
	tr := New(Config{Enabled: true, PrefixDepth: 3, SurfacedEnabled: false})
	// Deterministic workload: mix of prefixes, most pointing at "abo",
	// some unique.
	for i := 0; i < 500; i++ {
		tr.Record("about", nil)
	}
	tr.Record("apricot", nil)
	tr.Record("banana", nil)
	tr.Record("cherry", nil)

	stats := tr.Stats()
	hot := tr.Hot(100)
	cold := tr.Cold(^uint64(0)) // everything
	dump := tr.DumpPrefixHits()

	if stats.TrackedPrefixes != len(dump) {
		t.Errorf("Stats.TrackedPrefixes=%d, len(DumpPrefixHits)=%d", stats.TrackedPrefixes, len(dump))
	}
	if len(hot) != len(dump) {
		t.Errorf("len(Hot)=%d, len(DumpPrefixHits)=%d", len(hot), len(dump))
	}
	if len(cold) != len(dump) {
		t.Errorf("len(Cold)=%d, len(DumpPrefixHits)=%d", len(cold), len(dump))
	}
	// Total hits across accessors must match Stats.TotalRecords.
	var hotSum, coldSum, dumpSum uint64
	for _, p := range hot {
		hotSum += p.Hits
	}
	for _, p := range cold {
		coldSum += p.Hits
	}
	for _, v := range dump {
		dumpSum += v
	}
	if hotSum != stats.TotalRecords || coldSum != stats.TotalRecords || dumpSum != stats.TotalRecords {
		t.Errorf("hit sums diverge: total=%d hot=%d cold=%d dump=%d",
			stats.TotalRecords, hotSum, coldSum, dumpSum)
	}
}

// TestRecord_ConcurrentMixedPaths combines both: many goroutines each using
// mixed short/long prefixes that truncate to "abo". Exercises any data-race
// window between truncate + Lock + map write.
func TestRecord_ConcurrentMixedPaths(t *testing.T) {
	tr := New(Config{Enabled: true, PrefixDepth: 3, SurfacedEnabled: false})

	const goroutines = 50
	const perGoroutine = 200
	queries := []string{"abo", "abou", "about", "aboutus"}
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				q := queries[(i+g)%len(queries)]
				tr.Record(q, nil)
			}
		}(g)
	}
	wg.Wait()

	hot := tr.Hot(10)
	aboCount := 0
	for _, p := range hot {
		if p.Prefix == "abo" {
			aboCount++
		}
	}
	if aboCount != 1 {
		msg := fmt.Sprintf("expected 1 map key for 'abo' after concurrent mixed-path Records, got %d", aboCount)
		for _, p := range hot {
			msg += fmt.Sprintf("\n  key=%q bytes=%x hits=%d", p.Prefix, []byte(p.Prefix), p.Hits)
		}
		t.Error(msg)
	}
}
