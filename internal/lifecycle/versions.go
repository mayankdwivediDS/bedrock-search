// Package lifecycle manages corpus versions, staging, apply, and rollback
// (plan.md §11). It is intentionally independent from the HTTP server:
// every operation is a plain function returning (result, error) so it's
// trivially testable in isolation.
//
// On-disk layout managed here:
//
//	<list>/
//	    current.version
//	    versions/
//	        v1/, v2/, ...        (retained per CorpusVersionsKept)
//	        staging/             (pre-apply workspace)
package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go-suggest-neo/internal/corpus"
)

// Paths bundles the conventional paths for one list.
type Paths struct {
	ListDir     string // <data>/<list>
	VersionsDir string // <data>/<list>/versions
	StagingDir  string // <data>/<list>/versions/staging
	CurrentFile string // <data>/<list>/current.version
}

// PathsFor derives a Paths bundle from the list directory.
func PathsFor(listDir string) Paths {
	vdir := filepath.Join(listDir, corpus.DirVersions)
	return Paths{
		ListDir:     listDir,
		VersionsDir: vdir,
		StagingDir:  filepath.Join(vdir, corpus.DirStaging),
		CurrentFile: filepath.Join(listDir, corpus.FileCurrentVersion),
	}
}

// ListVersions returns the sorted list of "v<N>" version dirs (ascending
// by number). Non-version entries (like "staging") are skipped.
func ListVersions(p Paths) ([]string, error) {
	entries, err := os.ReadDir(p.VersionsDir)
	if err != nil {
		return nil, err
	}
	type vs struct {
		name string
		n    int
	}
	var out []vs
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "v") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "v"))
		if err != nil {
			continue
		}
		out = append(out, vs{e.Name(), n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].n < out[j].n })
	names := make([]string, len(out))
	for i, v := range out {
		names[i] = v.name
	}
	return names, nil
}

// ReadCurrent returns the value of current.version, trimmed.
func ReadCurrent(p Paths) (string, error) {
	b, err := os.ReadFile(p.CurrentFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// WriteCurrent atomically replaces current.version with the new value
// (tmp + rename). This is the single commit point of a version swap.
func WriteCurrent(p Paths, version string) error {
	tmp := p.CurrentFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.CurrentFile)
}

// NextVersion computes the next v<N+1> based on the highest existing
// version under versions/.
func NextVersion(p Paths) (string, error) {
	names, err := ListVersions(p)
	if err != nil {
		return "", err
	}
	highest := 0
	for _, name := range names {
		n, _ := strconv.Atoi(strings.TrimPrefix(name, "v"))
		if n > highest {
			highest = n
		}
	}
	return fmt.Sprintf("v%d", highest+1), nil
}

// Retain deletes all but the most-recent `keep` versions, preserving
// current.version no matter what. Returns the list of deleted version
// names.
func Retain(p Paths, keep int) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("keep must be >= 1")
	}
	names, err := ListVersions(p)
	if err != nil {
		return nil, err
	}
	cur, _ := ReadCurrent(p)
	if len(names) <= keep {
		return nil, nil
	}
	// Drop from the front (oldest) until <= keep remain.
	toDelete := names[:len(names)-keep]
	var deleted []string
	for _, name := range toDelete {
		if name == cur {
			// Never delete the active version, even if it is somehow
			// among the oldest (shouldn't happen if apply bumps first).
			continue
		}
		if err := os.RemoveAll(filepath.Join(p.VersionsDir, name)); err != nil {
			return deleted, err
		}
		deleted = append(deleted, name)
	}
	return deleted, nil
}

// DeleteVersion removes `versions/<name>/` and everything inside it. This
// is a one-way action — the version is not recoverable afterward, so the
// function guards against several footguns:
//
//   - name cannot be empty, "staging", or contain a path separator
//     (use DeleteStaging for the staging workspace; never traverse out of
//     the versions dir)
//   - name cannot equal current.version (would break serving immediately
//     and prevent the live engine from finding its corpus)
//   - name must point at an actually-existing version directory
//
// Returns the path that was removed so callers can log it.
func DeleteVersion(p Paths, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("version name must not be empty")
	}
	if strings.ContainsAny(name, `/\.`) {
		return "", fmt.Errorf("invalid version name %q", name)
	}
	if name == corpus.DirStaging {
		return "", fmt.Errorf("use DeleteStaging to remove the staging workspace")
	}
	cur, _ := ReadCurrent(p)
	if name == cur {
		return "", fmt.Errorf("refusing to delete current version %q — rollback or apply first", name)
	}
	dir := filepath.Join(p.VersionsDir, name)
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("version %q not found", name)
		}
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return dir, err
	}
	return dir, nil
}

// DeleteStaging removes the in-progress staging workspace if present.
// Safe — it's scratch space, never the live version. Idempotent: calling
// on a non-existent staging returns an error but nothing is changed.
func DeleteStaging(p Paths) (string, error) {
	if !StagingExists(p) {
		return "", fmt.Errorf("no staging present")
	}
	if err := os.RemoveAll(p.StagingDir); err != nil {
		return p.StagingDir, err
	}
	return p.StagingDir, nil
}

// Rollback flips current.version to an existing retained version.
// Returns the previous version so the caller can log it.
func Rollback(p Paths, to string) (prev string, err error) {
	versionDir := filepath.Join(p.VersionsDir, to)
	if info, serr := os.Stat(versionDir); serr != nil || !info.IsDir() {
		return "", fmt.Errorf("target version %q not found at %s", to, versionDir)
	}
	// Sanity-check that the required files are there.
	for _, f := range []string{corpus.FileCorpusSorted, corpus.FileCorpusIdx} {
		if _, serr := os.Stat(filepath.Join(versionDir, f)); serr != nil {
			return "", fmt.Errorf("target %s missing %s: %w", to, f, serr)
		}
	}
	prev, _ = ReadCurrent(p)
	if err := WriteCurrent(p, to); err != nil {
		return prev, err
	}
	return prev, nil
}
