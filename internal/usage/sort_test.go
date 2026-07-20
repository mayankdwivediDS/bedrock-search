package usage

import (
	"fmt"
	"sort"
	"testing"
)

// TestSortDescByHits_ReportsAllInputsUnchangedInCount verifies that the
// Shell-sort implementation (which duplicated entries in the live server's
// /usage/hot output) preserves the set of input entries — it should only
// reorder, never add, never drop.
func TestSortDescByHits_ReportsAllInputsUnchangedInCount(t *testing.T) {
	tests := []struct {
		name string
		in   []PrefixCount
	}{
		{
			name: "single hot + many ties at 1",
			in: []PrefixCount{
				{"abo", 207}, {"amr", 1}, {"aes", 1}, {"car", 1}, {"con", 1},
				{"ale", 1}, {"abc", 1}, {"ari", 1}, {"bra", 1},
			},
		},
		{
			name: "all equal hits",
			in: []PrefixCount{
				{"zzz", 5}, {"mmm", 5}, {"aaa", 5}, {"kkk", 5},
			},
		},
		{
			name: "alternating high/low",
			in: []PrefixCount{
				{"a", 1}, {"b", 100}, {"c", 1}, {"d", 100}, {"e", 1}, {"f", 100},
			},
		},
		{
			name: "two elements",
			in: []PrefixCount{{"lo", 1}, {"hi", 99}},
		},
		{
			name: "nine items with nested ordering",
			in: []PrefixCount{
				{"a", 9}, {"b", 8}, {"c", 7}, {"d", 6}, {"e", 5},
				{"f", 4}, {"g", 3}, {"h", 2}, {"i", 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := make(map[string]uint64, len(tt.in))
			for _, p := range tt.in {
				before[p.Prefix] = p.Hits
			}

			s := make([]PrefixCount, len(tt.in))
			copy(s, tt.in)
			sortDescByHits(s)

			if len(s) != len(tt.in) {
				t.Fatalf("length changed: before=%d after=%d", len(tt.in), len(s))
			}
			after := make(map[string]uint64, len(s))
			for _, p := range s {
				if _, dup := after[p.Prefix]; dup {
					t.Errorf("DUPLICATE after sort: %q appears twice", p.Prefix)
				}
				after[p.Prefix] = p.Hits
			}
			for k, v := range before {
				if after[k] != v {
					t.Errorf("key %q: before hits=%d, after hits=%d", k, v, after[k])
				}
			}
			// And descending by hits (strict).
			if !sort.SliceIsSorted(s, func(i, j int) bool { return s[i].Hits > s[j].Hits }) {
				msg := "not sorted desc:"
				for _, p := range s {
					msg += fmt.Sprintf(" %q=%d", p.Prefix, p.Hits)
				}
				t.Error(msg)
			}
		})
	}
}
