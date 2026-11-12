package creds

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// fileStore keeps every profile in one file protected per OS (see file_unix.go, file_windows.go).
type fileStore struct{ path string }

type fileContents struct {
	Default  string              `json:"default"`
	Profiles map[string]*Profile `json:"profiles"`
}

func (s *fileStore) load() (*fileContents, error) {
	c := &fileContents{Profiles: map[string]*Profile{}}
	raw, err := readSecure(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	return c, json.Unmarshal(raw, c)
}

func (s *fileStore) save(c *fileContents) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(c, "", "  ")
	return writeSecure(s.path, raw)
}

func (s *fileStore) Get(name string) (*Profile, error) {
	c, err := s.load()
	if err != nil {
		return nil, err
	}
	p, ok := c.Profiles[name]
	if !ok {
		return nil, ErrNotFound
	}
	return p, nil
}

func (s *fileStore) Put(p *Profile) error {
	c, err := s.load()
	if err != nil {
		return err
	}
	c.Profiles[p.Name] = p
	if c.Default == "" {
		c.Default = p.Name
	}
	return s.save(c)
}

func (s *fileStore) Delete(name string) error {
	c, err := s.load()
	if err != nil {
		return err
	}
	delete(c.Profiles, name)
	if c.Default == name {
		c.Default = ""
		for n := range c.Profiles {
			c.Default = n
			break
		}
	}
	return s.save(c)
}

func (s *fileStore) List() ([]string, error) {
	c, err := s.load()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

func (s *fileStore) Default() (string, error) {
	c, err := s.load()
	if err != nil {
		return "", err
	}
	return c.Default, nil
}

func (s *fileStore) SetDefault(name string) error {
	c, err := s.load()
	if err != nil {
		return err
	}
	c.Default = name
	return s.save(c)
}
