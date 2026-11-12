package bulk

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReadCSVMapsHeadersAndOverrides(t *testing.T) {
	rows, err := ReadCSV(strings.NewReader("Mobile, First Name,content\n+447700900001,Ann,hi\n+447700900002,,yo\n"), map[string]string{"mobile": "recipient"})
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	if rows[0]["recipient"] != "+447700900001" || rows[0]["firstname"] != "Ann" || rows[1]["content"] != "yo" {
		t.Fatal(rows)
	}
	if _, ok := rows[1]["firstname"]; ok {
		t.Fatal("empty cells must be omitted")
	}
}

func TestImportIDIsDeterministic(t *testing.T) {
	h, _ := FileHash(bytes.NewReader([]byte("abc")))
	a, b := ImportID(h, "42"), ImportID(h, "42")
	if a != b || len(a) != 32 || ImportID(h, "43") == a {
		t.Fatal(a, b)
	}
}

func TestRunRetriesTransportErrorWithSameKeyAndResumes(t *testing.T) {
	dir := t.TempDir()
	m, _ := LoadManifest(dir, "imp")
	m.ChunkSize = 2
	var keys []string
	calls := 0
	err := m.Run(5, func(chunk int, key string, start, end int) (ChunkResult, error) {
		calls++
		keys = append(keys, key)
		if chunk == 1 && calls == 2 {
			return ChunkResult{}, ErrTransport
		}
		if chunk == 2 {
			return ChunkResult{}, errors.New("boom")
		}
		return ChunkResult{BatchID: key, Accepted: end - start, Errors: []RowError{{Row: 1, Error: "bad"}}}, nil
	})
	if err == nil || err.Error() != "boom" {
		t.Fatal(err)
	}
	if strings.Join(keys, " ") != "imp-0 imp-1 imp-1 imp-2" {
		t.Fatal(keys)
	}
	// Rerun resumes from the manifest: only chunk 2 is sent.
	m2, _ := LoadManifest(dir, "imp")
	m2.ChunkSize = 2
	var again []string
	if err := m2.Run(5, func(chunk int, key string, start, end int) (ChunkResult, error) {
		again = append(again, key)
		return ChunkResult{Accepted: end - start}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(again, " ") != "imp-2" {
		t.Fatal(again)
	}
	res := m2.Results()
	if len(res) != 3 || res[0].Errors[0].Row != 2 || res[1].Errors[0].Row != 4 {
		t.Fatalf("%+v", res)
	}
}
