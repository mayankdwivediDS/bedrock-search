package normalise

import "regexp"

// Acceptance policy for corpus ingestion.
//
// Normalisation (see normalise.go) makes a keyword *consistent*; Accept decides
// whether a keyword is *clean enough to suggest at all*. It runs on the already
// normalised form (lower-cased, accent-stripped, trimmed) at every ingestion
// chokepoint, so junk never reaches the corpus and therefore never surfaces as
// a search suggestion.
//
// The rules below were derived by profiling the real advertiser feed
// (~1.44M names): the list is overwhelmingly clean, so the policy is
// deliberately narrow — it rejects only unambiguous junk and keeps every
// ordinary brand name (apostrophes, ampersands, dots, digits, CJK, etc.).

// rejectRe matches a normalised keyword that should be rejected. A match on
// any alternative is enough to drop the entry. RE2-compatible (no lookaround).
var rejectRe = regexp.MustCompile(`(?i)` +
	`https?://|www\.` + // URLs
	`|\.(?:com|net|org|co|in|io)\d*\b` + // domain suffixes, esp. scraped "*.comNN" garbage
	`|\\u[0-9a-f]{4}` + // leftover \uXXXX escape sequences (broken encoding)
	`|[\x00-\x1f]`) // control characters / embedded newlines

// hasLetterRe is satisfied when the keyword contains at least one Unicode
// letter. Entries with no letters at all (pure digits/symbols) are rejected.
var hasLetterRe = regexp.MustCompile(`\pL`)

// Accept reports whether a normalised keyword is clean enough to ingest.
// It returns false for empty strings, strings with no letters, and strings
// matching any junk pattern in rejectRe.
func Accept(s string) bool {
	if s == "" {
		return false
	}
	if !hasLetterRe.MatchString(s) {
		return false
	}
	return !rejectRe.MatchString(s)
}
