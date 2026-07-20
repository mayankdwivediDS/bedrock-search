package lifecycle

import (
	"bufio"
	"io"
	"os"
	"path/filepath"

	"go-suggest-neo/internal/corpus"
)

// DiffResult quantifies how a staged corpus compares against the current
// live version. Exact (not estimated) — a two-pointer merge of sorted
// files, O(n) in total word count.
type DiffResult struct {
	Added    int64 `json:"added"`    // in staging, not in current
	Dropped  int64 `json:"dropped"`  // in current, not in staging
	Retained int64 `json:"retained"` // in both
}

// Diff compares two sorted corpus files line-by-line. Both files must
// already be sorted (they will be — Bootstrap produces sorted output).
func Diff(p Paths, currentVersion string) (*DiffResult, error) {
	curPath := filepath.Join(p.VersionsDir, currentVersion, corpus.FileCorpusSorted)
	stagePath := filepath.Join(p.StagingDir, corpus.FileCorpusSorted)

	curF, err := os.Open(curPath)
	if err != nil {
		return nil, err
	}
	defer curF.Close()
	stF, err := os.Open(stagePath)
	if err != nil {
		return nil, err
	}
	defer stF.Close()

	curR := bufio.NewReaderSize(curF, 1<<20)
	stR := bufio.NewReaderSize(stF, 1<<20)

	var res DiffResult
	a, aOK, err := readLine(curR)
	if err != nil {
		return nil, err
	}
	b, bOK, err := readLine(stR)
	if err != nil {
		return nil, err
	}
	for aOK && bOK {
		switch {
		case a < b:
			res.Dropped++
			a, aOK, err = readLine(curR)
		case a > b:
			res.Added++
			b, bOK, err = readLine(stR)
		default:
			res.Retained++
			a, aOK, err = readLine(curR)
			if err != nil {
				return nil, err
			}
			b, bOK, err = readLine(stR)
		}
		if err != nil {
			return nil, err
		}
	}
	// Drain tails.
	for aOK {
		res.Dropped++
		a, aOK, err = readLine(curR)
		if err != nil {
			return nil, err
		}
		_ = a
	}
	for bOK {
		res.Added++
		b, bOK, err = readLine(stR)
		if err != nil {
			return nil, err
		}
		_ = b
	}
	return &res, nil
}

// readLine reads one line (without trailing \n) from r. Returns "", false
// on clean EOF. Mirrors corpus.readLine but is copied here to keep
// lifecycle independent of corpus internals.
func readLine(r *bufio.Reader) (string, bool, error) {
	line, err := r.ReadString('\n')
	if err == io.EOF {
		if line == "" {
			return "", false, nil
		}
		return stripNL(line), true, nil
	}
	if err != nil {
		return "", false, err
	}
	return stripNL(line), true, nil
}

func stripNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
