// Package normalise applies a consistent query/keyword normalisation used by
// both corpus ingestion and runtime queries.
//
// Normalisation pipeline:
//  1. Unicode NFD decomposition, strip combining marks (accents), NFC recompose.
//  2. ASCII-lowercase.
//  3. Trim surrounding whitespace.
//
// The pipeline is cheap per-call but allocates internally; callers that
// normalise millions of strings at bootstrap time should reuse a single
// Normaliser to avoid churning transform chains.
package normalise

import (
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Normaliser holds a preallocated transform chain. Safe for concurrent use.
type Normaliser struct {
	pool sync.Pool
}

// New returns a reusable Normaliser. The transform chain is pooled so
// bootstrap (one goroutine at a time) and runtime queries (many goroutines)
// both pay minimal allocation overhead.
func New() *Normaliser {
	return &Normaliser{
		pool: sync.Pool{
			New: func() any {
				return transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
			},
		},
	}
}

// String normalises s: strips accents, lowercases, trims.
// Returns "" if the transform fails (invalid UTF-8 etc).
func (n *Normaliser) String(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	t := n.pool.Get().(transform.Transformer)
	defer func() {
		t.Reset()
		n.pool.Put(t)
	}()
	out, _, err := transform.String(t, s)
	if err != nil {
		return ""
	}
	return strings.ToLower(out)
}

// defaultNormaliser is a package-level singleton for convenience callers.
var defaultNormaliser = New()

// String normalises using the default package-level Normaliser.
func String(s string) string { return defaultNormaliser.String(s) }
