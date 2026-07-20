package server

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-suggest-neo/internal/normalise"
)

// The blacklist is a persistent, normalised set of words that must never
// appear in suggestions. It is stored as one word per line in
// <listDir>/blacklist.txt and is applied in two places:
//
//   - /upload filters incoming CSV values against it, so blacklisted words
//     are never added in the first place.
//   - /blacklist?reload=true rebuilds the live corpus without them, so words
//     already present are removed too.

func blacklistPath(listDir string) string {
	return filepath.Join(listDir, "blacklist.txt")
}

// loadBlacklistSet reads the blacklist into a normalised set. A missing file
// is not an error — it just means nothing is blacklisted yet.
func loadBlacklistSet(listDir string) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	f, err := os.Open(blacklistPath(listDir))
	if err != nil {
		if os.IsNotExist(err) {
			return set, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		w := normalise.String(strings.TrimSpace(sc.Text()))
		if w != "" {
			set[w] = struct{}{}
		}
	}
	return set, sc.Err()
}

// sortedBlacklist returns the current blacklist as a sorted slice (for GET).
func sortedBlacklist(listDir string) ([]string, error) {
	set, err := loadBlacklistSet(listDir)
	if err != nil {
		return nil, err
	}
	return setToSorted(set), nil
}

// addToBlacklist merges words into the blacklist file (normalised, deduped,
// sorted) and returns how many were newly added plus the new total.
func addToBlacklist(listDir string, words []string) (added, total int, err error) {
	set, err := loadBlacklistSet(listDir)
	if err != nil {
		return 0, 0, err
	}
	before := len(set)
	for _, w := range words {
		n := normalise.String(strings.TrimSpace(w))
		if n != "" {
			set[n] = struct{}{}
		}
	}
	if err := writeBlacklist(listDir, set); err != nil {
		return 0, 0, err
	}
	return len(set) - before, len(set), nil
}

func writeBlacklist(listDir string, set map[string]struct{}) error {
	list := setToSorted(set)
	body := ""
	if len(list) > 0 {
		body = strings.Join(list, "\n") + "\n"
	}
	tmp := blacklistPath(listDir) + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, blacklistPath(listDir))
}

func setToSorted(set map[string]struct{}) []string {
	list := make([]string, 0, len(set))
	for w := range set {
		list = append(list, w)
	}
	sort.Strings(list)
	return list
}
