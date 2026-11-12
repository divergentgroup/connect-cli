//go:build windows

package creds

import (
	"fmt"
	"os"

	"github.com/billgraziano/dpapi"
	"golang.org/x/sys/windows"
)

// readSecure decrypts the DPAPI-protected file; any failure refuses the fallback.
func readSecure(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	plain, err := dpapi.DecryptBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("refusing to read %s: DPAPI decryption failed: %w", path, err)
	}
	return plain, nil
}

// writeSecure encrypts with DPAPI for the current user and applies a user-only DACL.
func writeSecure(path string, data []byte) error {
	enc, err := dpapi.EncryptBytes(data)
	if err != nil {
		return fmt.Errorf("DPAPI encryption failed: %w", err)
	}
	if err := os.WriteFile(path, enc, 0o600); err != nil {
		return err
	}
	if err := restrictToCurrentUser(path); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("could not restrict %s to the current user: %w", path, err)
	}
	return nil
}

func restrictToCurrentUser(path string) error {
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid)},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
