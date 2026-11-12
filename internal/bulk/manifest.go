package bulk

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const ChunkSize = 500

// ErrTransport marks a lost response: the chunk is retried once with the same idempotency key.
var ErrTransport = errors.New("transport error")

type RowError struct {
	Row   int    `json:"row"` // 1-based row in the original file, header excluded
	Error string `json:"error"`
}

type ChunkResult struct {
	BatchID  string     `json:"batchId,omitempty"`
	Accepted int        `json:"accepted"`
	Errors   []RowError `json:"errors,omitempty"`
}

// Manifest records each chunk's outcome so a rerun resumes instead of resending.
type Manifest struct {
	ImportID  string                 `json:"importId"`
	ChunkSize int                    `json:"chunkSize"`
	Chunks    map[string]ChunkResult `json:"chunks"`
	dir       string
}

func manifestPath(dir, id string) string { return filepath.Join(dir, "imports", id+".json") }

// LoadManifest returns the saved manifest for an import id, or a fresh one.
func LoadManifest(dir, id string) (*Manifest, error) {
	m := &Manifest{ImportID: id, ChunkSize: ChunkSize, Chunks: map[string]ChunkResult{}, dir: dir}
	raw, err := os.ReadFile(manifestPath(dir, id))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, m); err != nil {
		return nil, err
	}
	if m.Chunks == nil {
		m.Chunks = map[string]ChunkResult{}
	}
	return m, nil
}

func (m *Manifest) Save() error {
	p := manifestPath(m.dir, m.ImportID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(p, raw, 0o600)
}

// Key is the Idempotency-Key for chunk n.
func (m *Manifest) Key(n int) string { return fmt.Sprintf("%s-%d", m.ImportID, n) }

// Results returns completed chunk results in chunk order.
func (m *Manifest) Results() []ChunkResult {
	out := make([]ChunkResult, 0, len(m.Chunks))
	for n := 0; ; n++ {
		r, ok := m.Chunks[strconv.Itoa(n)]
		if !ok {
			return out
		}
		out = append(out, r)
	}
}

// Run sends rows [0,total) in chunks. send receives the chunk index, its idempotency key and the
// row range; row errors it returns are chunk-relative and get mapped to file rows here. Completed
// chunks in the manifest are skipped; a transport error is retried once with the same key.
func (m *Manifest) Run(total int, send func(chunk int, key string, start, end int) (ChunkResult, error)) error {
	for n, start := 0, 0; start < total; n, start = n+1, start+m.ChunkSize {
		if _, done := m.Chunks[strconv.Itoa(n)]; done {
			continue
		}
		end := min(start+m.ChunkSize, total)
		res, err := send(n, m.Key(n), start, end)
		if errors.Is(err, ErrTransport) {
			res, err = send(n, m.Key(n), start, end)
		}
		if err != nil {
			_ = m.Save()
			return err
		}
		for i := range res.Errors {
			res.Errors[i].Row += start + 1
		}
		m.Chunks[strconv.Itoa(n)] = res
		if err := m.Save(); err != nil {
			return err
		}
	}
	return nil
}
