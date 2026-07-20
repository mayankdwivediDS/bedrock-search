package server

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// renameRetry renames old→new, retrying a few times. On Windows a directory
// rename can briefly fail with "Access is denied" while the OS finishes
// releasing recently-closed file handles or an antivirus/indexer lets go; a
// short backoff clears it.
func renameRetry(oldPath, newPath string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = os.Rename(oldPath, newPath); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

// writeBackupZip streams a zip of everything under root to w. Paths inside
// the archive are relative to root's parent, so the archive contains e.g.
// "data/default/current.version" and restore can recreate the same layout.
//
// Scratch files (.tmp directories, the empty-seed marker) are skipped so the
// backup stays clean and restore never trips over a half-written file.
func writeBackupZip(w io.Writer, root string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()

	base := filepath.Dir(root)
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if skipFromBackup(path, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		fw, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(fw, f)
		return err
	})
}

// skipFromBackup reports whether a path should be excluded from the archive.
func skipFromBackup(path string, info os.FileInfo) bool {
	name := info.Name()
	if info.IsDir() && (name == ".tmp" || name == "staging") {
		return true
	}
	if strings.HasSuffix(name, ".tmp") || name == ".empty.seed.json" {
		return true
	}
	return false
}

// extractZip unpacks a zip archive into destDir, guarding against zip-slip
// (entries whose path escapes destDir).
func extractZip(zipPath, destDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	cleanDest := filepath.Clean(destDir)
	for _, f := range zr.File {
		target := filepath.Join(cleanDest, filepath.FromSlash(f.Name))
		// Reject any entry that would land outside destDir.
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in zip: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractOne(f, target); err != nil {
			return err
		}
	}
	return nil
}

func extractOne(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}
