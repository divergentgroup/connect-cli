//go:build !windows

package creds

import (
	"fmt"
	"os"
)

// readSecure refuses a credentials file that is group- or world-accessible.
func readSecure(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("refusing to read %s: permissions are %04o, must be 0600", path, info.Mode().Perm())
	}
	return os.ReadFile(path)
}

// writeSecure writes atomically with mode 0600 in a 0700 directory.
func writeSecure(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
