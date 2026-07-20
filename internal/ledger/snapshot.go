package ledger

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"os"
)

// WriteSnapshot gob-encodes the ledger's current state to path atomically:
// write to tmp + fsync + rename. Atomicity matters because a partial file
// would be rejected on the next load, costing us the warm-restart benefit.
func WriteSnapshot(l *Ledger, path string) error {
	entries := l.Snapshot()
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	bw := bufio.NewWriterSize(f, 1<<16)
	if err := gob.NewEncoder(bw).Encode(entries); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("gob encode: %w", err)
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// ReadSnapshot loads a snapshot previously written by WriteSnapshot. A
// missing file is not an error; the caller treats nil,nil as "cold start".
func ReadSnapshot(path string) ([]SnapshotEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var entries []SnapshotEntry
	if err := gob.NewDecoder(bufio.NewReader(f)).Decode(&entries); err != nil {
		return nil, err
	}
	return entries, nil
}
