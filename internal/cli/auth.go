package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/auth"
	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/divergentgroup/connect-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newAuth(app *App) *cobra.Command {
	c := &cobra.Command{Use: "auth", Short: "Sign in, inspect or remove credentials"}
	c.AddCommand(newAuthLogin(app), newAuthStatus(app), newAuthLogout(app), newAuthSignup(app))
	return c
}

func newAuthLogin(app *App) *cobra.Command {
	var apiKey, noBrowser bool
	var port int
	c := &cobra.Command{
		Use:   "login",
		Short: "Sign in with your browser, or store a workspace API key",
		Long: `Signs in with your divergent account in the browser (OAuth with PKCE) and stores the grant in the OS keychain.

With --api-key the key is read from stdin (pipe it, or use "-") or a hidden prompt on a TTY.
There is no flag that takes the key inline: keys must never land in shell history or agent transcripts.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := app.store()
			if err != nil {
				return err
			}
			var p *creds.Profile
			if apiKey {
				key, err := readSecret(app, "API key: ")
				if err != nil {
					return err
				}
				p = &creds.Profile{Kind: creds.KindAPIKey, APIKey: key}
			} else {
				if p, err = auth.Login(cmd.Context(), auth.LoginOptions{Issuer: app.issuer(), NoBrowser: noBrowser, CallbackPort: port, In: app.In, Out: app.Err, OpenBrowser: openBrowser}); err != nil {
					return apierr.Auth(err.Error())
				}
			}
			summary, err := app.summary(cmd.Context(), &creds.Credential{Profile: p})
			if err != nil {
				// A key or grant that cannot even read the workspace summary is useless; but an API
				// that lacks the endpoint (older deployment) should not lose a successful sign-in.
				var ae *apierr.Error
				if !errors.As(err, &ae) || ae.Code != "not_found" {
					return err
				}
				fmt.Fprintf(app.Err, "Warning: could not read the workspace summary (%s); saving the credential without a workspace name.\n", output.Clean(ae.Message))
				summary = &api.WorkspaceDto{Id: p.WorkspaceID, Name: p.WorkspaceID}
			}
			p.WorkspaceName = summary.Name
			if p.WorkspaceID == "" {
				p.WorkspaceID = summary.Id
			}
			p.Name = app.Profile
			if p.Name == "" {
				p.Name = creds.ProfileName(summary.Name)
			}
			if err := store.Put(p); err != nil {
				return err
			}
			if err := store.SetDefault(p.Name); err != nil {
				return err
			}
			if p.Kind == creds.KindAPIKey {
				fmt.Fprintf(app.Out, "Stored API key for %s as profile %q.\n", output.Clean(summary.Name), p.Name)
			} else {
				fmt.Fprintf(app.Out, "Signed in to %s with scopes %s.\n", output.Clean(summary.Name), strings.Join(p.Scopes, " "))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&apiKey, "api-key", false, "store a workspace API key read from stdin or a prompt")
	c.Flags().BoolVar(&noBrowser, "no-browser", false, "print the sign-in URL and paste the redirect back (SSH/headless)")
	c.Flags().IntVar(&port, "callback-port", 0, "pin the loopback callback port (for ssh -L)")
	return c
}

// readSecret reads one line from stdin when piped, else a hidden TTY prompt. Never echoes.
func readSecret(app *App, prompt string) (string, error) {
	if f, ok := app.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(app.Err, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(app.Err)
		if err != nil {
			return "", err
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
		return "", apierr.Usage("no API key entered")
	}
	line, _ := bufio.NewReader(app.In).ReadString('\n')
	if s := strings.TrimSpace(line); s != "" {
		return s, nil
	}
	return "", apierr.Usage("no API key on stdin; pipe it in, e.g. `pass show connect | connect auth login --api-key`")
}

// summary fetches GET /v2/workspace for a credential.
func (app *App) summary(ctx context.Context, cred *creds.Credential) (*api.WorkspaceDto, error) {
	u, err := app.apiURL()
	if err != nil {
		return nil, err
	}
	c, err := api.NewClient(u, api.WithRequestEditorFn(cred.ApplyV2), api.WithHTTPClient(app.httpClient()))
	if err != nil {
		return nil, err
	}
	var s api.WorkspaceDto
	resp, err := c.V2GetWorkspace(ctx, nil)
	wasJSON := app.JSON
	app.JSON = false
	defer func() { app.JSON = wasJSON }()
	return &s, app.finish(resp, err, func(body []byte) error { return json.Unmarshal(body, &s) })
}

func newAuthStatus(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which credential and workspace connect is using",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cred, err := app.credential(cmd.Context())
			if err != nil {
				return err
			}
			p := cred.Profile
			st := map[string]any{"source": cred.Source, "profile": p.Name, "kind": p.Kind, "workspaceId": p.WorkspaceID, "workspaceName": p.WorkspaceName, "apiUrl": config.APIURL(app.APIURL)}
			if p.Kind == creds.KindToken {
				st["scopes"] = p.Scopes
				st["expiresAt"] = p.ExpiresAt.UTC().Format(time.RFC3339)
			}
			if app.JSON {
				return json.NewEncoder(app.Out).Encode(st)
			}
			pairs := [][2]string{{"Credential", cred.Source}, {"Kind", p.Kind}, {"Workspace", joinNonEmpty(p.WorkspaceName, p.WorkspaceID)}, {"API", config.APIURL(app.APIURL)}}
			if p.Kind == creds.KindToken {
				pairs = append(pairs, [2]string{"Scopes", strings.Join(p.Scopes, " ")}, [2]string{"Token expires", p.ExpiresAt.Local().Format(time.RFC1123)})
			}
			app.printer().KV(pairs)
			return nil
		},
	}
}

func newAuthLogout(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke the grant and delete the profile",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := app.store()
			if err != nil {
				return err
			}
			name := app.Profile
			if name == "" {
				if name, err = store.Default(); err != nil {
					return err
				}
			}
			p, err := store.Get(name)
			if errors.Is(err, creds.ErrNotFound) {
				fmt.Fprintln(app.Out, "Not signed in.")
				return nil
			}
			if err != nil {
				return err
			}
			if p.Kind == creds.KindToken && p.RefreshToken != "" {
				if err := auth.Revoke(cmd.Context(), app.issuer(), p.RefreshToken); err != nil {
					fmt.Fprintf(app.Err, "Could not revoke the grant (%v); removing it locally. You can also remove it under Connected apps on your account page.\n", err)
				}
			}
			if err := store.Delete(name); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Signed out of %s.\n", name)
			return nil
		},
	}
}

func newAuthSignup(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "signup",
		Short: "Create a workspace (coming soon)",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(app.Out, "Agent-driven signup is coming soon. Create a workspace at https://new.puresms.app and run `connect auth login`.")
			return nil
		},
	}
}
