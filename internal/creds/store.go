package creds

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/divergentgroup/connect-cli/internal/config"
)

var ErrNotFound = errors.New("no credentials found; run `connect auth login`")

type Store interface {
	Get(name string) (*Profile, error)
	Put(p *Profile) error
	Delete(name string) error
	List() ([]string, error)
	Default() (string, error)
	SetDefault(name string) error
}

// Open returns the keyring store when the OS keyring works, otherwise the secured file store.
func Open(noKeyring bool) (Store, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return nil, err
	}
	if !noKeyring && keyringAvailable() {
		return &keyringStore{index: indexFile{path: filepath.Join(dir, "profiles.json")}}, nil
	}
	return &fileStore{path: filepath.Join(dir, "credentials.json")}, nil
}

// indexFile lists profile names and the default; it holds no secrets.
type indexFile struct{ path string }

type index struct {
	Default string   `json:"default"`
	Names   []string `json:"profiles"`
}

func (f indexFile) load() (index, error) {
	var ix index
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return ix, nil
	}
	if err != nil {
		return ix, err
	}
	return ix, json.Unmarshal(raw, &ix)
}

func (f indexFile) save(ix index) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(ix, "", "  ")
	return os.WriteFile(f.path, raw, 0o600)
}

func (f indexFile) add(name string) error {
	ix, err := f.load()
	if err != nil {
		return err
	}
	for _, n := range ix.Names {
		if n == name {
			return nil
		}
	}
	ix.Names = append(ix.Names, name)
	if ix.Default == "" {
		ix.Default = name
	}
	return f.save(ix)
}

func (f indexFile) remove(name string) error {
	ix, err := f.load()
	if err != nil {
		return err
	}
	kept := ix.Names[:0]
	for _, n := range ix.Names {
		if n != name {
			kept = append(kept, n)
		}
	}
	ix.Names = kept
	if ix.Default == name {
		ix.Default = ""
		if len(kept) > 0 {
			ix.Default = kept[0]
		}
	}
	return f.save(ix)
}

// NewFileStore is exposed for tests and for --no-keyring callers that already know the path.
func NewFileStore(path string) Store { return &fileStore{path: path} }
