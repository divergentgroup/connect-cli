// Package creds stores and resolves credential profiles.
package creds

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/config"
)

const (
	KindToken  = "token"
	KindAPIKey = "apikey"
)

type Profile struct {
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	WorkspaceID   string    `json:"workspaceId,omitempty"`
	WorkspaceName string    `json:"workspaceName,omitempty"`
	Scopes        []string  `json:"scopes,omitempty"`
	AccessToken   string    `json:"accessToken,omitempty"`
	RefreshToken  string    `json:"refreshToken,omitempty"`
	ExpiresAt     time.Time `json:"expiresAt,omitempty"`
	APIKey        string    `json:"apiKey,omitempty"`
}

var notName = regexp.MustCompile(`[^a-z0-9-]+`)

// ProfileName turns any text into a safe profile name: lower case letters, digits and hyphens only.
// Names become file names (lock files), so nothing else may get through.
func ProfileName(s string) string {
	n := strings.Trim(notName.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if n == "" {
		return "default"
	}
	return n
}

// Fresh reports whether the access token is usable for at least another minute.
func (p *Profile) Fresh() bool { return time.Until(p.ExpiresAt) > time.Minute }

// Credential is a resolved profile plus where it came from.
type Credential struct {
	Profile           *Profile
	Source            string // "env CONNECT_API_KEY" or "profile <name>"
	WorkspaceOverride string // --workspace, API-key mode only
}

// Apply sets the credential headers for the hosted MCP server. Never both credentials.
func (c *Credential) Apply(_ context.Context, req *http.Request) error {
	req.Header.Del("Authorization")
	req.Header.Del("X-Api-Key")
	req.Header.Del("X-Workspace-Id")
	if c.Profile.Kind == KindAPIKey {
		req.Header.Set("X-Api-Key", c.Profile.APIKey)
		if c.WorkspaceOverride != "" {
			req.Header.Set("X-Workspace-Id", c.WorkspaceOverride)
		}
		return nil
	}
	if c.WorkspaceOverride != "" {
		return apierr.Usage("--workspace only applies to API-key credentials; a signed-in token is bound to one workspace")
	}
	req.Header.Set("Authorization", "Bearer "+c.Profile.AccessToken)
	req.Header.Set("X-Workspace-Id", c.Profile.WorkspaceID)
	return nil
}

// ApplyV2 sets the headers for the Connect v2 API: every credential travels as a bearer (v2 refuses
// X-Api-Key) and the API version is pinned. Apply stays as it is for the MCP server.
func (c *Credential) ApplyV2(ctx context.Context, req *http.Request) error {
	if err := c.Apply(ctx, req); err != nil {
		return err
	}
	if c.Profile.Kind == KindAPIKey {
		req.Header.Del("X-Api-Key")
		req.Header.Set("Authorization", "Bearer "+c.Profile.APIKey)
	}
	req.Header.Set("Connect-Version", config.APIVersion)
	return nil
}

// Workspace is the human label for status lines.
func (c *Credential) Workspace() string {
	if c.WorkspaceOverride != "" {
		return c.WorkspaceOverride
	}
	if c.Profile.WorkspaceName != "" {
		return c.Profile.WorkspaceName
	}
	return c.Profile.WorkspaceID
}
