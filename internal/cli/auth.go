package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
			var p *creds.Profile
			var err error
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
			if _, err := saveProfile(app, cmd, p, false); err != nil {
				return err
			}
			if p.Kind == creds.KindAPIKey {
				fmt.Fprintf(app.Out, "Stored API key for %s as profile %q.\n", output.Clean(p.WorkspaceName), p.Name)
			} else {
				fmt.Fprintf(app.Out, "Signed in to %s with scopes %s.\n", output.Clean(p.WorkspaceName), strings.Join(p.Scopes, " "))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&apiKey, "api-key", false, "store a workspace API key read from stdin or a prompt")
	c.Flags().BoolVar(&noBrowser, "no-browser", false, "print the sign-in URL and paste the redirect back (SSH/headless)")
	c.Flags().IntVar(&port, "callback-port", 0, "pin the loopback callback port (for ssh -L)")
	return c
}

// saveProfile names a signed-in profile after its workspace and stores it as the default.
// With tolerant, any failure to read the workspace summary still saves the credential; degraded reports that.
func saveProfile(app *App, cmd *cobra.Command, p *creds.Profile, tolerant bool) (degraded bool, err error) {
	store, err := app.store()
	if err != nil {
		return false, err
	}
	summary, err := app.summary(cmd.Context(), &creds.Credential{Profile: p})
	if err != nil {
		// A key or grant that cannot even read the workspace summary is useless; but an API
		// that lacks the endpoint (older deployment) should not lose a successful sign-in.
		var ae *apierr.Error
		if !errors.As(err, &ae) {
			ae = &apierr.Error{Message: err.Error()}
		}
		if !tolerant && ae.Code != "not_found" {
			return false, err
		}
		degraded = true
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
		return false, err
	}
	return degraded, store.SetDefault(p.Name)
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

const signupHelp = `Creates a workspace and signs in, one step at a time. The server decides what comes next (a code to enter, a browser step) and its message says what to do.

Ask the person for each code. Show them https://divergent.group/legal/online-services-agreement and pass --agree-terms only once they agree.

Continue with "connect auth signup verify CODE", or "connect auth signup resend" if the code did not arrive.`

func newAuthSignup(app *App) *cobra.Command {
	var req auth.StartRequest
	c := &cobra.Command{
		Use:   "signup",
		Short: "Create a workspace and sign in (email and phone codes)",
		Long:  signupHelp,
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("name") && !cmd.Flags().Changed("email") && !cmd.Flags().Changed("phone") && !cmd.Flags().Changed("workspace") {
				if _, err := loadSignup(); err == nil {
					fmt.Fprintln(app.Out, "A sign-up is already in progress. Use `connect auth signup verify CODE` or `connect auth signup resend`, or pass the flags to start over.")
					return nil
				}
			}
			st, step, err := auth.SignupStart(cmd.Context(), app.issuer(), req)
			if err != nil {
				return apierr.Upstream(err)
			}
			if st.ID != "" {
				if err := saveSignup(st); err != nil {
					return err
				}
			}
			return signupStep(app, cmd, st, step)
		},
	}
	c.Flags().StringVar(&req.Name, "name", "", "the person's name")
	c.Flags().StringVar(&req.Email, "email", "", "the person's email address")
	c.Flags().StringVar(&req.Phone, "phone", "", "the person's mobile number")
	c.Flags().StringVar(&req.WorkspaceName, "workspace", "", "name for the new workspace")
	c.Flags().StringVar(&req.Brand, "brand", "", "connect or puresms (default if omitted)")
	c.Flags().BoolVar(&req.AgreeTerms, "agree-terms", false, "the person has read and agreed to the online services agreement")
	c.Flags().BoolVar(&req.AllowSetup, "allow-setup", false, "also allow set-up actions (sender names, email domains, test recipients)")

	verify := &cobra.Command{
		Use:   "verify CODE",
		Short: "Submit the code the person received",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadSignup()
			if err != nil {
				return err
			}
			step, err := auth.SignupCode(cmd.Context(), app.issuer(), st, strings.TrimSpace(args[0]))
			if err != nil {
				return apierr.Upstream(err)
			}
			return signupStep(app, cmd, st, step)
		},
	}
	resend := &cobra.Command{
		Use:   "resend",
		Short: "Send the current code again",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := loadSignup()
			if err != nil {
				return err
			}
			step, err := auth.SignupResend(cmd.Context(), app.issuer(), st)
			if err != nil {
				return apierr.Upstream(err)
			}
			return signupStep(app, cmd, st, step)
		},
	}
	c.AddCommand(verify, resend)
	return c
}

// signupStep reports a server answer and acts on what it says comes next. The server owns the rules.
func signupStep(app *App, cmd *cobra.Command, st *auth.SignupState, step *auth.Step) error {
	msg := output.Clean(step.Message)
	switch {
	case step.Status == http.StatusNotFound:
		removeSignup()
		return &apierr.Error{Code: "not_found", Message: msg + " If you already finished signing up, run `connect auth status`; otherwise start again with `connect auth signup`."}
	case step.Status == http.StatusBadRequest:
		return &apierr.Error{Code: "validation", Message: msg}
	case step.Status != http.StatusOK:
		// 409 (busy) and anything unexpected: the state file is kept so the same call can be retried.
		return &apierr.Error{Code: "upstream", Message: msg}
	}
	result := map[string]any{"next": step.Next, "message": msg}
	switch step.Next {
	case "browser", "restart":
		removeSignup()
	case "done":
		p, err := auth.SignupFinish(cmd.Context(), app.issuer(), st, step.Credential)
		if err != nil {
			return apierr.Auth(err.Error())
		}
		// The credential is single use: save it before anything else can fail, and only then drop the state.
		degraded, err := saveProfile(app, cmd, p, true)
		if err != nil {
			return err
		}
		removeSignup()
		if degraded {
			hint := "Signed in, but the workspace summary could not be read; run `connect workspace show` to check."
			result["hint"] = hint
			defer func() {
				if !app.JSON {
					fmt.Fprintln(app.Out, hint)
				}
			}()
		}
		result["profile"], result["workspaceId"], result["workspaceName"] = p.Name, p.WorkspaceID, p.WorkspaceName
	}
	if app.JSON {
		return json.NewEncoder(app.Out).Encode(result)
	}
	fmt.Fprintln(app.Out, msg)
	return nil
}

func signupPath() (string, error) {
	d, err := config.ConfigDir()
	return filepath.Join(d, "signup.json"), err
}

func loadSignup() (*auth.SignupState, error) {
	p, err := signupPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	var st auth.SignupState
	if err != nil || json.Unmarshal(b, &st) != nil || st.ID == "" {
		return nil, apierr.Usage("no sign-up in progress; start one with `connect auth signup --name ... --email ... --phone ... --workspace ... --agree-terms`")
	}
	return &st, nil
}

func saveSignup(st *auth.SignupState) error {
	p, err := signupPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	_ = os.Remove(p) // a pre-existing file would keep its old mode
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(st)
}

func removeSignup() {
	if p, err := signupPath(); err == nil {
		_ = os.Remove(p)
	}
}
