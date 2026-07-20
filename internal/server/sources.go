package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const sourceHistoryFile = "source-files.json"

// SourceFile is an audit record for one CSV ingested into a project. The
// source data itself is never retained by the server after ingestion.
type SourceFile struct {
	Name       string    `json:"name"`
	Column     string    `json:"column"`
	Mode       string    `json:"mode"`
	ValuesRead int       `json:"values_read"`
	WordCount  int64     `json:"corpus_words"`
	Version    string    `json:"version"`
	ImportedAt time.Time `json:"imported_at"`
}

var sourceHistoryMu sync.Mutex

func readSourceFiles(listDir string) ([]SourceFile, error) {
	sourceHistoryMu.Lock()
	defer sourceHistoryMu.Unlock()
	return readSourceFilesLocked(listDir)
}

func readSourceFilesLocked(listDir string) ([]SourceFile, error) {
	b, err := os.ReadFile(filepath.Join(listDir, sourceHistoryFile))
	if os.IsNotExist(err) {
		return []SourceFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var files []SourceFile
	if err := json.Unmarshal(b, &files); err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ImportedAt.After(files[j].ImportedAt) })
	return files, nil
}

func appendSourceFile(listDir string, file SourceFile) error {
	sourceHistoryMu.Lock()
	defer sourceHistoryMu.Unlock()
	files, err := readSourceFilesLocked(listDir)
	if err != nil {
		return err
	}
	files = append(files, file)
	b, err := json.MarshalIndent(files, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(listDir, sourceHistoryFile), b, 0o600)
}
