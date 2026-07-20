package usage

import (
	"path/filepath"
	"testing"
)

func TestRecord_PrefixTruncation(t *testing.T) {
	t1 := New(Config{Enabled: true, PrefixDepth: 3, SurfacedEnabled: false})
	t1.Record("marketing", []string{"marketing"})
	t1.Record("market", []string{"market"})
	t1.Record("mar", []string{"mar"}) // exactly the depth boundary

	st := t1.Stats()
	if st.TrackedPrefixes != 1 {
		t.Errorf("depth-3 truncation should collapse all three into 1 bucket, got %d",
			st.TrackedPrefixes)
	}
	hot := t1.Hot(10)
	if len(hot) != 1 || hot[0].Prefix != "mar" || hot[0].Hits != 3 {
		t.Errorf("Hot = %v, want [{mar 3}]", hot)
	}
}

func TestSurfacedSetAndCold(t *testing.T) {
	tr := New(Config{Enabled: true, PrefixDepth: 2, SurfacedEnabled: true})
	tr.Record("ap", []string{"apple", "apricot"})
	tr.Record("ba", []string{"banana"})
	tr.Record("ba", []string{"banana"}) // 2 hits on "ba"

	if !tr.WasSurfaced("apple") {
		t.Error("apple should be marked surfaced")
	}
	if tr.WasSurfaced("zzz") {
		t.Error("zzz should not be surfaced")
	}

	cold := tr.Cold(1) // prefixes with <=1 hit → just "ap"
	if len(cold) != 1 || cold[0].Prefix != "ap" {
		t.Errorf("Cold(1) = %v, want [{ap 1}]", cold)
	}
}

func TestDisabled_NoOps(t *testing.T) {
	tr := New(Config{Enabled: false})
	tr.Record("mar", []string{"marketing"})
	st := tr.Stats()
	if st.TotalRecords != 0 || st.TrackedPrefixes != 0 || st.SurfacedWords != 0 {
		t.Errorf("disabled tracker should be inert, got %+v", st)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	tr := New(Config{Enabled: true, PrefixDepth: 3, SurfacedEnabled: true})
	tr.Record("mar", []string{"marketing", "market"})
	tr.Record("mar", []string{"marketing"})
	tr.Record("app", []string{"apple"})

	path := filepath.Join(t.TempDir(), "usage.stats.gob")
	if err := Write(tr, path); err != nil {
		t.Fatalf("write: %v", err)
	}
	snap, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if snap == nil {
		t.Fatal("expected snap, got nil")
	}

	fresh := New(Config{Enabled: true, PrefixDepth: 3, SurfacedEnabled: true})
	Restore(fresh, snap)
	st := fresh.Stats()
	if st.TrackedPrefixes != 2 {
		t.Errorf("after restore, tracked = %d, want 2", st.TrackedPrefixes)
	}
	if st.SurfacedWords != 3 {
		t.Errorf("after restore, surfaced = %d, want 3", st.SurfacedWords)
	}
	if !fresh.WasSurfaced("marketing") {
		t.Error("marketing should be surfaced after restore")
	}
}
