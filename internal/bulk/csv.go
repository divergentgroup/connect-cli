// Package bulk parses CSV input and runs resumable, idempotent chunked uploads.
package bulk

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// ReadCSV returns one map per data row keyed by field name. Header columns map to fields
// case-insensitively (spaces, dashes and underscores ignored); overrides are column→field.
func ReadCSV(r io.Reader, overrides map[string]string) ([]map[string]string, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("reading CSV header: %w", err)
	}
	fields := make([]string, len(header))
	for i, h := range header {
		fields[i] = normalise(h)
		for col, f := range overrides {
			if normalise(col) == fields[i] {
				fields[i] = normalise(f)
			}
		}
	}
	var rows []map[string]string
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading CSV row %d: %w", len(rows)+1, err)
		}
		row := map[string]string{}
		for i, v := range rec {
			if i < len(fields) && strings.TrimSpace(v) != "" {
				row[fields[i]] = strings.TrimSpace(v)
			}
		}
		rows = append(rows, row)
	}
}

func normalise(s string) string {
	return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(s)))
}

// ImportID derives a stable id from the file content and the target workspace.
func ImportID(fileSHA256 []byte, workspaceID string) string {
	sum := sha256.Sum256(append(append([]byte{}, fileSHA256...), []byte(workspaceID)...))
	return hex.EncodeToString(sum[:])[:32]
}

// FileHash returns the SHA-256 of a reader's contents.
func FileHash(r io.Reader) ([]byte, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
