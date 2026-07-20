package ledger

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestLedger(thresh uint32, win, ttl time.Duration) *Ledger {
	return New(Config{Window: win, Threshold: thresh, IdleTTL: ttl})
}

func TestRecord_ThresholdCrossingFiresOnceThenStays(t *testing.T) {
	l := newTestLedger(3, time.Minute, time.Hour)
	if l.Record("mar") {
		t.Fatal("first record must not cross")
	}
	if l.Record("mar") {
		t.Fatal("second record must not cross")
	}
	if !l.Record("mar") {
		t.Fatal("third record must cross threshold")
	}
	if l.Record("mar") {
		t.Fatal("fourth record must not re-cross in same window")
	}
	if l.Record("mar") {
		t.Fatal("fifth record must not re-cross either")
	}
}

func TestRecord_DifferentPrefixesAreIndependent(t *testing.T) {
	l := newTestLedger(2, time.Minute, time.Hour)
	if l.Record("mar") {
		t.Fatal("mar #1 should not cross")
	}
	if l.Record("ada") {
		t.Fatal("ada #1 should not cross")
	}
	if !l.Record("mar") {
		t.Fatal("mar #2 should cross (threshold=2)")
	}
	if !l.Record("ada") {
		t.Fatal("ada #2 should cross independently")
	}
}

func TestRecord_WindowResetAllowsReCross(t *testing.T) {
	l := newTestLedger(2, 10*time.Millisecond, time.Hour)
	if l.Record("hot") {
		t.Fatal("first record should not cross")
	}
	if !l.Record("hot") {
		t.Fatal("second record should cross")
	}
	// Sleep past the window to force a reset on the next Record.
	time.Sleep(30 * time.Millisecond)
	if l.Record("hot") {
		t.Fatal("post-window record should not cross on its own (count resets)")
	}
	if !l.Record("hot") {
		t.Fatal("next record should cross in the new window")
	}
}

func TestSweep_DropsIdleEntries(t *testing.T) {
	l := newTestLedger(1000, time.Minute, 10*time.Millisecond)
	l.Record("one")
	l.Record("two")
	l.Record("three")
	if got := l.Size(); got != 3 {
		t.Fatalf("size before sweep = %d, want 3", got)
	}
	time.Sleep(30 * time.Millisecond)
	removed := l.Sweep()
	if removed != 3 {
		t.Fatalf("expected to sweep 3, got %d", removed)
	}
	if got := l.Size(); got != 0 {
		t.Fatalf("size after sweep = %d, want 0", got)
	}
}

func TestMarkAndClearPromoted(t *testing.T) {
	l := newTestLedger(100, time.Minute, time.Hour)
	l.MarkPromoted("xyz")
	// MarkPromoted creates the counter; subsequent Record should not re-
	// cross because the counter is already flagged as promoted.
	if l.Record("xyz") {
		t.Fatal("MarkPromoted should suppress subsequent Crossed signals")
	}
	l.ClearPromoted("xyz")
	// Force crossing under a low threshold ledger.
	lowL := newTestLedger(1, time.Minute, time.Hour)
	lowL.MarkPromoted("abc")
	if lowL.Record("abc") {
		t.Fatal("marked-promoted record still suppressed")
	}
	lowL.ClearPromoted("abc")
	// After clear, the counter's promoted flag is reset, but its hits
	// count is still 1 from the earlier Record (which bumped hits to 1
	// and set promoted=true internally). The next Record will see
	// hits>=threshold and !promoted, so it fires.
	if !lowL.Record("abc") {
		t.Fatal("after ClearPromoted a fresh Record should be able to cross again")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	l := newTestLedger(2, time.Minute, time.Hour)
	l.Record("one")
	l.Record("two")
	l.Record("two") // crosses

	path := filepath.Join(t.TempDir(), "ledger.snapshot.gob")
	if err := WriteSnapshot(l, path); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	fresh := newTestLedger(2, time.Minute, time.Hour)
	loaded := fresh.Restore(entries)
	if loaded != 2 {
		t.Errorf("restored %d, want 2", loaded)
	}
	if got := fresh.HotPrefixes(); len(got) != 1 || got[0] != "two" {
		t.Errorf("HotPrefixes after restore = %v, want [two]", got)
	}
}

func TestReadSnapshot_MissingFileIsNotError(t *testing.T) {
	entries, err := ReadSnapshot(filepath.Join(t.TempDir(), "does-not-exist.gob"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if entries != nil {
		t.Fatalf("missing file should return nil, got %v", entries)
	}
}

func TestRestore_SkipsExpiredEntries(t *testing.T) {
	l := newTestLedger(2, time.Minute, 10*time.Millisecond)
	// Hand-craft entries with very old LastSeen.
	entries := []SnapshotEntry{
		{Prefix: "fresh", LastSeen: time.Now()},
		{Prefix: "stale", LastSeen: time.Now().Add(-time.Hour)},
	}
	loaded := l.Restore(entries)
	if loaded != 1 {
		t.Errorf("should have loaded only the fresh entry, got %d", loaded)
	}
	if l.Size() != 1 {
		t.Errorf("ledger size = %d, want 1", l.Size())
	}
}
