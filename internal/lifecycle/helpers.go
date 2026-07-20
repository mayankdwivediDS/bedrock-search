package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"go-suggest-neo/internal/corpus"
)

// writeManifestFile serialises man to versionDir/manifest.json atomically.
// Used by Apply to rewrite staging's placeholder manifest with the real
// version tag + mode once the rename completes.
func writeManifestFile(versionDir string, man *corpus.Manifest) error {
	raw, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(versionDir, corpus.FileManifest)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// sortSlice is a thin wrapper so migration code can sort a []string
// without importing sort itself. Kept separate to keep apply.go focused
// on the migration logic rather than stdlib shims.
func sortSlice(s []string) { sort.Strings(s) }
