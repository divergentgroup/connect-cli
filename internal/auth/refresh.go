package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/gofrs/flock"
	"golang.org/x/oauth2"
)

var inflight sync.Mutex // single-flight within the process

// Refresh returns a fresh profile, exchanging the refresh token under a cross-process lock
// so concurrent invocations perform exactly one exchange (the identity provider rolls refresh tokens).
func Refresh(ctx context.Context, store creds.Store, p *creds.Profile, issuer string) (*creds.Profile, error) {
	if p.Kind != creds.KindToken || p.Fresh() {
		return p, nil
	}
	inflight.Lock()
	defer inflight.Unlock()
	dir, err := config.StateDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(dir, creds.ProfileName(p.Name)+".lock"))
	if err := lock.Lock(); err != nil {
		return nil, err
	}
	defer lock.Unlock()
	if latest, err := store.Get(p.Name); err == nil && latest.Fresh() {
		return latest, nil
	} else if err == nil {
		p = latest
	}
	tok, err := oauthConfig(issuer, "").TokenSource(ctx, &oauth2.Token{RefreshToken: p.RefreshToken}).Token()
	if err != nil {
		return nil, apierr.Auth("your session has expired; run `connect auth login`")
	}
	fresh := ProfileFromToken(tok)
	fresh.Name, fresh.WorkspaceName = p.Name, p.WorkspaceName
	if fresh.WorkspaceID == "" {
		fresh.WorkspaceID = p.WorkspaceID
	}
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = p.RefreshToken
	}
	if err := store.Put(fresh); err != nil {
		return nil, err
	}
	return fresh, nil
}

// Revoke asks the identity provider to revoke the refresh token.
func Revoke(ctx context.Context, issuer, refreshToken string) error {
	form := url.Values{"token": {refreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {config.ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/connect/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("revocation returned %s", resp.Status)
	}
	return nil
}
