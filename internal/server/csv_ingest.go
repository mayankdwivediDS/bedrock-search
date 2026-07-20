package server

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"go-suggest-neo/internal/normalise"
)

// csvColumnToJSONArray reads a CSV file, picks one column by header name, and
// writes its non-empty values as a JSON array of strings to outJSONPath —
// exactly the format the bootstrap pipeline ingests.
//
// The column is matched case-insensitively against the header row. Empty
// cells and ragged rows are tolerated and skipped. Duplicate values are NOT
// removed here — the bootstrap sort+merge dedups within the file and the
// merge-apply dedups against the existing corpus, so every path is covered.
//
// Values whose normalised form is in `drop` (the blacklist) are skipped, so
// blacklisted words are never ingested in the first place.
//
// Returns the number of values written.
func csvColumnToJSONArray(csvPath, column, outJSONPath string, drop map[string]struct{}) (int, error) {
	in, err := os.Open(csvPath)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	r := csv.NewReader(bufio.NewReaderSize(in, 1<<20))
	r.FieldsPerRecord = -1 // tolerate rows with varying field counts
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return 0, fmt.Errorf("read CSV header: %w", err)
	}
	col := -1
	want := strings.TrimSpace(column)
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), want) {
			col = i
			break
		}
	}
	if col == -1 {
		return 0, fmt.Errorf("column %q not found in CSV; available columns: %s",
			column, strings.Join(header, ", "))
	}

	out, err := os.Create(outJSONPath)
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriterSize(out, 1<<20)
	if err := bw.WriteByte('['); err != nil {
		out.Close()
		return 0, err
	}

	count := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			out.Close()
			return 0, fmt.Errorf("read CSV row %d: %w", count+1, err)
		}
		if col >= len(rec) {
			continue // row is too short for this column
		}
		val := strings.TrimSpace(rec[col])
		if val == "" {
			continue
		}
		if len(drop) > 0 {
			if _, bad := drop[normalise.String(val)]; bad {
				continue // blacklisted — never ingest it
			}
		}
		enc, err := json.Marshal(val)
		if err != nil {
			out.Close()
			return 0, err
		}
		if count > 0 {
			if err := bw.WriteByte(','); err != nil {
				out.Close()
				return 0, err
			}
		}
		if _, err := bw.Write(enc); err != nil {
			out.Close()
			return 0, err
		}
		count++
	}

	if err := bw.WriteByte(']'); err != nil {
		out.Close()
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		out.Close()
		return 0, err
	}
	return count, out.Close()
}
