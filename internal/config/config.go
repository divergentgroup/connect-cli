// Package config holds build-time values and fixed identity constants.
package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
)

// Set at release time via goreleaser ldflags.
var (
	Version       = "dev"
	DefaultAPIURL = "https://api.connect.ms"
)

// Fixed regardless of API origin.
const (
	Issuer      = "https://idp.divergent.group"
	ClientID    = "connect-cli"
	Resource    = "https://mcp.connect.ms/mcp"
	MCPEndpoint = "https://mcp.connect.ms/mcp"
	DocsURL     = "https://connect.ms/docs/cli"
	BillingURL  = "https://new.puresms.app/settings/billing"
	// APIVersion is sent as Connect-Version on every request so a later dated revision of v2 never changes what the CLI sees.
	APIVersion = "2026-10-01"
)

var Scopes = []string{
	"connect:sms.read", "connect:sms.send", "connect:sms.send.live",
	"connect:contacts.read", "connect:contacts.write", "connect:reports.read",
	"connect:sms.senders.read", "connect:sms.senders.write",
	"connect:email.read", "connect:email.send", "connect:email.send.live",
	"openid", "offline_access",
}

// APIURL resolves --api-url, then CONNECT_API_URL, then the compiled default.
func APIURL(flag string) string {
	if flag != "" {
		return flag
	}
	if v := os.Getenv("CONNECT_API_URL"); v != "" {
		return v
	}
	return DefaultAPIURL
}

// CheckOrigin refuses anything but https, except plain http to this machine for local development.
func CheckOrigin(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("is not an absolute URL")
	}
	if h := u.Hostname(); u.Scheme == "https" || u.Scheme == "http" && (h == "localhost" || net.ParseIP(h).IsLoopback()) {
		return nil
	}
	return errors.New("must use https (plain http is only allowed for localhost)")
}

func ConfigDir() (string, error) { return dir("XDG_CONFIG_HOME", ".config") }
func StateDir() (string, error)  { return dir("XDG_STATE_HOME", filepath.Join(".local", "state")) }

func dir(xdg, fallback string) (string, error) {
	if runtime.GOOS == "windows" {
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "connect"), nil
		}
	} else if v := os.Getenv(xdg); v != "" {
		return filepath.Join(v, "connect"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallback, "connect"), nil
}
