package creds

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestApplyTokenHeaders(t *testing.T) {
	c := &Credential{Profile: &Profile{Kind: KindToken, AccessToken: "jwt", WorkspaceID: "42"}}
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := c.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "Bearer jwt" || req.Header.Get("X-Workspace-Id") != "42" || req.Header.Get("X-Api-Key") != "" {
		t.Fatal(req.Header)
	}
	c.WorkspaceOverride = "7"
	if err := c.Apply(context.Background(), req); err == nil {
		t.Fatal("--workspace must be refused for token credentials")
	}
}

func TestApplyKeyHeaders(t *testing.T) {
	c := &Credential{Profile: &Profile{Kind: KindAPIKey, APIKey: "k"}, WorkspaceOverride: "7"}
	req, _ := http.NewRequest("GET", "http://x", nil)
	req.Header.Set("Authorization", "Bearer stale")
	_ = c.Apply(context.Background(), req)
	if req.Header.Get("X-Api-Key") != "k" || req.Header.Get("Authorization") != "" || req.Header.Get("X-Workspace-Id") != "7" {
		t.Fatal(req.Header)
	}
}

func TestApplyV2SendsEveryCredentialAsBearerWithVersion(t *testing.T) {
	key := &Credential{Profile: &Profile{Kind: KindAPIKey, APIKey: "k"}}
	req, _ := http.NewRequest("GET", "http://x", nil)
	_ = key.ApplyV2(context.Background(), req)
	if req.Header.Get("Authorization") != "Bearer k" || req.Header.Get("X-Api-Key") != "" || req.Header.Get("Connect-Version") != "2026-10-01" {
		t.Fatal(req.Header)
	}
	tok := &Credential{Profile: &Profile{Kind: KindToken, AccessToken: "jwt", WorkspaceID: "42"}}
	req, _ = http.NewRequest("GET", "http://x", nil)
	_ = tok.ApplyV2(context.Background(), req)
	if req.Header.Get("Authorization") != "Bearer jwt" || req.Header.Get("X-Workspace-Id") != "42" || req.Header.Get("Connect-Version") == "" {
		t.Fatal(req.Header)
	}
}

func TestResolveEnvWins(t *testing.T) {
	s := &fileStore{path: filepath.Join(t.TempDir(), "credentials.json")}
	_ = s.Put(&Profile{Name: "acme", Kind: KindToken, AccessToken: "jwt"})
	t.Setenv("CONNECT_API_KEY", "k")
	c, err := Resolve(s, "acme", "")
	if err != nil || c.Source != "env CONNECT_API_KEY" || c.Profile.Kind != KindAPIKey {
		t.Fatal(c, err)
	}
	t.Setenv("CONNECT_API_KEY", "")
	c, err = Resolve(s, "", "")
	if err != nil || c.Source != "profile acme" {
		t.Fatal(c, err)
	}
	if _, err := Resolve(s, "missing", ""); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	s := &fileStore{path: filepath.Join(t.TempDir(), "sub", "credentials.json")}
	if err := s.Put(&Profile{Name: "a", Kind: KindAPIKey, APIKey: "k", ExpiresAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = s.Put(&Profile{Name: "b", Kind: KindToken})
	names, _ := s.List()
	def, _ := s.Default()
	if strings.Join(names, ",") != "a,b" || def != "a" {
		t.Fatal(names, def)
	}
	_ = s.SetDefault("b")
	_ = s.Delete("b")
	if def, _ = s.Default(); def != "a" {
		t.Fatal(def)
	}
	if p, err := s.Get("a"); err != nil || p.APIKey != "k" {
		t.Fatal(p, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(s.path); info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %04o", info.Mode().Perm())
		}
		_ = os.Chmod(s.path, 0o644)
		if _, err := s.Get("a"); err == nil || !strings.Contains(err.Error(), "permissions") {
			t.Fatal("world-readable file must be refused", err)
		}
	}
}

func TestProfileNameKeepsOnlySafeCharacters(t *testing.T) {
	for in, want := range map[string]string{"../../.ssh/x": "ssh-x", "Acme Ltd.": "acme-ltd", "O'Neill & Co": "o-neill-co", "///": "default"} {
		if got := ProfileName(in); got != want {
			t.Error(in, got)
		}
	}
}
