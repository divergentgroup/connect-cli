package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/auth"
	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/divergentgroup/connect-cli/internal/output"
	"github.com/spf13/cobra"
)

const scopeAPIKeyOnly = "api-key-only"

func (a *App) printer() output.Printer { return output.New(a.Out, a.JSON) }

func (a *App) store() (creds.Store, error) {
	if a.Store != nil {
		return a.Store, nil
	}
	return creds.Open(a.NoKeyring)
}

// credential resolves and, for token profiles, refreshes the credential to use.
func (a *App) credential(ctx context.Context) (*creds.Credential, error) {
	store, err := a.store()
	if err != nil {
		return nil, err
	}
	cred, err := creds.Resolve(store, a.Profile, a.Workspace)
	if errors.Is(err, creds.ErrNotFound) {
		return nil, apierr.Auth(err.Error())
	}
	if err != nil {
		return nil, err
	}
	if cred.Profile.Kind == creds.KindToken {
		if cred.Profile, err = auth.Refresh(ctx, store, cred.Profile, a.issuer()); err != nil {
			return nil, err
		}
	}
	return cred, nil
}

func (a *App) issuer() string {
	if a.Issuer != "" {
		return a.Issuer
	}
	return config.Issuer
}

// client builds the API client for a command, refusing api-key-only commands under a token profile.
func (a *App) client(cmd *cobra.Command) (*api.Client, *creds.Credential, error) {
	cred, err := a.credential(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	if cmd.Annotations["scope"] == scopeAPIKeyOnly && cred.Profile.Kind != creds.KindAPIKey {
		return nil, nil, &apierr.Error{Code: "insufficient_scope", Message: fmt.Sprintf("`%s` needs a workspace API key; it is not available to signed-in grants. Run `connect auth login --api-key`.", cmd.CommandPath())}
	}
	u, err := a.apiURL()
	if err != nil {
		return nil, nil, err
	}
	c, err := api.NewClient(u, api.WithRequestEditorFn(cred.ApplyV2), api.WithHTTPClient(a.httpClient()))
	return c, cred, err
}

// origin refuses an insecure override and says once per run when a non-default host is in use.
func (a *App) origin(label, u, def string) error {
	if err := config.CheckOrigin(u); err != nil {
		return apierr.Usage(fmt.Sprintf("%s %s %v", label, u, err))
	}
	if u != def && !a.announced[label] {
		if a.announced == nil {
			a.announced = map[string]bool{}
		}
		a.announced[label] = true
		fmt.Fprintf(a.Err, "Using %s %s\n", label, u)
	}
	return nil
}

// apiURL is the checked API origin.
func (a *App) apiURL() (string, error) {
	u := config.APIURL(a.APIURL)
	return u, a.origin("API", u, config.DefaultAPIURL)
}

func (a *App) httpClient() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

// finish maps the response through the error contract, then prints raw JSON or calls human(body).
func (a *App) finish(resp *http.Response, err error, human func(body []byte) error) error {
	if err != nil {
		return apierr.Upstream(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return apierr.Upstream(err)
	}
	if e := apierr.FromResponse(resp, body); e != nil {
		return e
	}
	if a.JSON {
		return a.printer().Raw(body)
	}
	if human == nil {
		return nil
	}
	return human(body)
}

// sendNotice tells the user which credential and workspace a send goes out under (design §Auth).
func (a *App) sendNotice(cred *creds.Credential) {
	fmt.Fprintf(a.Err, "Sending as %s (workspace %s)\n", output.Clean(cred.Source), output.Clean(cred.Workspace()))
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptr[T any](v T) *T { return &v }

// optional returns nil for the zero value so omitted flags stay off the wire.
func optional[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

func joinNonEmpty(parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}
