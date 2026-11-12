package creds

import (
	"os"
)

// Resolve picks the credential: CONNECT_API_KEY in the environment wins, then --profile, then the default profile.
func Resolve(store Store, profileFlag, workspaceFlag string) (*Credential, error) {
	if key := os.Getenv("CONNECT_API_KEY"); key != "" {
		return &Credential{Profile: &Profile{Name: "env", Kind: KindAPIKey, APIKey: key}, Source: "env CONNECT_API_KEY", WorkspaceOverride: workspaceFlag}, nil
	}
	name := profileFlag
	if name == "" {
		var err error
		if name, err = store.Default(); err != nil {
			return nil, err
		}
	}
	if name == "" {
		return nil, ErrNotFound
	}
	p, err := store.Get(name)
	if err != nil {
		return nil, err
	}
	return &Credential{Profile: p, Source: "profile " + name, WorkspaceOverride: workspaceFlag}, nil
}
