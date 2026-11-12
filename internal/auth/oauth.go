// Package auth implements the OAuth 2.1 flows the CLI uses against the divergent identity provider.
package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"golang.org/x/oauth2"
)

type LoginOptions struct {
	Issuer       string
	NoBrowser    bool
	CallbackPort int
	In           io.Reader
	Out          io.Writer
	Timeout      time.Duration
	OpenBrowser  func(string) error
}

func endpoint(issuer string) oauth2.Endpoint {
	return oauth2.Endpoint{AuthURL: issuer + "/connect/authorize", TokenURL: issuer + "/connect/token", AuthStyle: oauth2.AuthStyleInParams}
}

func oauthConfig(issuer, redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: config.ClientID, Endpoint: endpoint(issuer), Scopes: config.Scopes, RedirectURL: redirect}
}

// Login runs the PKCE authorization-code flow and returns an unnamed token profile.
func Login(ctx context.Context, o LoginOptions) (*creds.Profile, error) {
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, err
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	verifier := oauth2.GenerateVerifier()

	var ln net.Listener
	redirect := "http://127.0.0.1/callback"
	if !o.NoBrowser {
		var err error
		if ln, err = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(o.CallbackPort)); err != nil {
			return nil, err
		}
		defer ln.Close()
		redirect = fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	}
	conf := oauthConfig(o.Issuer, redirect)
	authURL := conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", config.Resource))

	var code string
	var err error
	if o.NoBrowser {
		code, err = pasteFlow(o, authURL, state)
	} else {
		fmt.Fprintln(o.Out, "Opening your browser to sign in. If nothing happens, rerun with --no-browser.")
		if o.OpenBrowser != nil {
			_ = o.OpenBrowser(authURL)
		}
		code, err = loopbackFlow(ctx, ln, state)
	}
	if err != nil {
		return nil, err
	}
	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	return ProfileFromToken(tok), nil
}

// ProfileFromToken reads the workspace and scopes off the access token.
func ProfileFromToken(tok *oauth2.Token) *creds.Profile {
	ws, scopes, exp := Claims(tok.AccessToken)
	if s, ok := tok.Extra("scope").(string); ok && s != "" {
		scopes = strings.Fields(s)
	}
	if !tok.Expiry.IsZero() {
		exp = tok.Expiry
	}
	return &creds.Profile{Kind: creds.KindToken, WorkspaceID: ws, Scopes: scopes, AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, ExpiresAt: exp}
}

// pasteFlow prints the authorize URL and reads the redirect URL the user pastes back.
func pasteFlow(o LoginOptions, authURL, state string) (string, error) {
	fmt.Fprintf(o.Out, "Open this URL in a browser on any machine:\n\n%s\n\nThe final page will fail to load at 127.0.0.1; paste its full address here.\nRedirect URL: ", authURL)
	line, err := bufio.NewReader(o.In).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no redirect URL provided")
	}
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		return "", err
	}
	return codeFromQuery(u.Query(), state)
}

func codeFromQuery(q url.Values, state string) (string, error) {
	if q.Get("state") != state {
		return "", errors.New("state mismatch")
	}
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("authorization refused: %s", e)
	}
	if q.Get("code") == "" {
		return "", errors.New("no code in redirect")
	}
	return q.Get("code"), nil
}

// loopbackFlow serves /callback until a request with the exact state arrives or ctx ends.
func loopbackFlow(ctx context.Context, ln net.Listener, state string) (string, error) {
	type result struct {
		code string
		err  error
	}
	got := make(chan result, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/callback" {
			http.Error(w, "Invalid callback", http.StatusBadRequest)
			return
		}
		q := r.URL.Query()
		code, err := codeFromQuery(q, state)
		refused := q.Get("state") == state && q.Get("error") != ""
		switch {
		case refused: // a genuine refusal ends the login now; error_description is never shown
			http.Error(w, "Sign-in was not completed. You can close this tab.", http.StatusForbidden)
		case err != nil:
			http.Error(w, "Invalid callback", http.StatusBadRequest)
			return
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, closePage())
		}
		select {
		case got <- result{code, err}:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	select {
	case r := <-got:
		return r.code, r.err
	case <-ctx.Done():
		return "", errors.New("timed out waiting for the browser sign-in")
	}
}

// Claims decodes the JWT payload without verification: the CLI trusts the issuer it fetched from.
func Claims(jwt string) (workspaceID string, scopes []string, exp time.Time) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return
	}
	var c struct {
		WorkspaceID any     `json:"workspace_id"`
		Scope       any     `json:"scope"`
		Exp         float64 `json:"exp"`
	}
	_ = json.Unmarshal(raw, &c)
	workspaceID = fmt.Sprint(c.WorkspaceID)
	if c.WorkspaceID == nil {
		workspaceID = ""
	}
	switch s := c.Scope.(type) {
	case string:
		scopes = strings.Fields(s)
	case []any:
		for _, v := range s {
			scopes = append(scopes, fmt.Sprint(v))
		}
	}
	if c.Exp > 0 {
		exp = time.Unix(int64(c.Exp), 0)
	}
	return
}
