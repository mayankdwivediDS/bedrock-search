// Command bootstrap turns a caller-supplied JSON array of strings into a
// versioned corpus under <data>/<list>/versions/vN/.
//
// Usage:
//
//	bootstrap -source path/to/keywords.json -data ./data -list default
//	bootstrap -source ... -version v2 -parent v1   # for a subsequent version
//
// The process is streaming + chunked: peak memory is bounded by -chunk-mb.
// Safe to run on multi-GB input files on a modest machine.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go-suggest-neo/internal/corpus"
)

func main() {
	var (
		source   = flag.String("source", "", "path to source JSON array of strings (required)")
		dataDir  = flag.String("data", "data", "root data directory")
		list     = flag.String("list", "default", "list name (subdirectory under data)")
		version  = flag.String("version", "", "version tag, e.g. v1 (default: next available)")
		parent   = flag.String("parent", "", "parent version tag (default: current.version if present)")
		mode     = flag.String("mode", "bootstrap", "bootstrap|replace|merge|prune — recorded in manifest")
		chunkMB  = flag.Int("chunk-mb", 256, "in-memory chunk size for external sort")
		stride   = flag.Int("stride", 4096, "skip-index stride (one anchor every N sorted lines)")
		logLevel = flag.String("log", "info", "debug|info|warn|error")
	)
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLogLevel(*logLevel),
	})))

	if *source == "" {
		slog.Error("-source is required")
		os.Exit(2)
	}
	if _, err := os.Stat(*source); err != nil {
		slog.Error("source not found", "err", err)
		os.Exit(2)
	}

	listDir := filepath.Join(*dataDir, *list)
	versionsDir := filepath.Join(listDir, corpus.DirVersions)
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		slog.Error("mkdir versions dir", "err", err)
		os.Exit(1)
	}

	// Resolve version + parent.
	resolvedVersion, resolvedParent, err := resolveVersion(listDir, versionsDir, *version, *parent)
	if err != nil {
		slog.Error("resolve version", "err", err)
		os.Exit(1)
	}

	outDir := filepath.Join(versionsDir, resolvedVersion)
	if _, err := os.Stat(outDir); err == nil {
		slog.Error("target version already exists; refusing to overwrite", "dir", outDir)
		os.Exit(1)
	}

	man, err := corpus.Bootstrap(corpus.BootstrapOptions{
		SourceJSON:    *source,
		OutDir:        outDir,
		SortChunkMB:   *chunkMB,
		SkipStride:    *stride,
		Version:       resolvedVersion,
		ParentVersion: resolvedParent,
		Mode:          *mode,
	})
	if err != nil {
		slog.Error("bootstrap failed", "err", err)
		// Best-effort cleanup of partial output.
		_ = os.RemoveAll(outDir)
		os.Exit(1)
	}
	idx, err := corpus.LoadSkipIndex(filepath.Join(outDir, corpus.FileCorpusIdx))
	if err != nil {
		slog.Warn("could not reload skip index for summary", "err", err)
	}

	// Update current.version pointer only on the first bootstrap. Subsequent
	// versions are applied via /corpus/apply at runtime, not by this CLI.
	curPath := filepath.Join(listDir, corpus.FileCurrentVersion)
	if _, err := os.Stat(curPath); os.IsNotExist(err) {
		if err := writeCurrentVersion(curPath, resolvedVersion); err != nil {
			slog.Warn("could not write current.version", "err", err)
		} else {
			slog.Info("current.version initialised", "version", resolvedVersion)
		}
	} else {
		slog.Info("current.version already present, not overwritten",
			"path", curPath,
			"hint", "use the runtime /corpus/apply endpoint to switch versions")
	}

	anchors := 0
	if idx != nil {
		anchors = len(idx.Entries)
	}
	fmt.Fprintf(os.Stdout,
		"bootstrapped %s: %d words, %d skip anchors, manifest at %s\n",
		resolvedVersion, man.WordCount, anchors, filepath.Join(outDir, corpus.FileManifest),
	)
}

func resolveVersion(listDir, versionsDir, wantVersion, wantParent string) (string, string, error) {
	// If caller specified both, take them as-is.
	if wantVersion != "" {
		return wantVersion, wantParent, nil
	}

	// Else, next version after the highest existing vN.
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return "", "", err
	}
	highest := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "v") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(name, "v"))
		if err != nil {
			continue
		}
		if n > highest {
			highest = n
		}
	}
	next := fmt.Sprintf("v%d", highest+1)

	// Parent resolves to current.version if the caller didn't set it.
	parent := wantParent
	if parent == "" {
		if b, err := os.ReadFile(filepath.Join(listDir, corpus.FileCurrentVersion)); err == nil {
			parent = strings.TrimSpace(string(b))
		}
	}
	return next, parent, nil
}

func writeCurrentVersion(path, version string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
