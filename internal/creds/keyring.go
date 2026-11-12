package creds

import (
	"encoding/json"
	"errors"

	"github.com/zalando/go-keyring"
)

const keyringService = "connect-cli"

func keyringAvailable() bool {
	if err := keyring.Set(keyringService, "__probe__", "1"); err != nil {
		return false
	}
	_ = keyring.Delete(keyringService, "__probe__")
	return true
}

type keyringStore struct{ index indexFile }

func (s *keyringStore) Get(name string) (*Profile, error) {
	raw, err := keyring.Get(keyringService, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var p Profile
	return &p, json.Unmarshal([]byte(raw), &p)
}

func (s *keyringStore) Put(p *Profile) error {
	raw, _ := json.Marshal(p)
	if err := keyring.Set(keyringService, p.Name, string(raw)); err != nil {
		return err
	}
	return s.index.add(p.Name)
}

func (s *keyringStore) Delete(name string) error {
	if err := keyring.Delete(keyringService, name); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return s.index.remove(name)
}

func (s *keyringStore) List() ([]string, error) {
	ix, err := s.index.load()
	return ix.Names, err
}

func (s *keyringStore) Default() (string, error) {
	ix, err := s.index.load()
	return ix.Default, err
}

func (s *keyringStore) SetDefault(name string) error {
	ix, err := s.index.load()
	if err != nil {
		return err
	}
	ix.Default = name
	return s.index.save(ix)
}
