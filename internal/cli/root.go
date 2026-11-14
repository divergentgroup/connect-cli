// Package cli defines the cobra command tree.
package cli

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/spf13/cobra"
)

// App carries global flags and output streams through the command tree.
type App struct {
	JSON      bool
	Profile   string
	Workspace string
	APIURL    string
	NoKeyring bool
	In        io.Reader
	Out, Err  io.Writer

	announced map[string]bool // origins already reported this run

	// Test seams; nil means production defaults.
	Store  creds.Store
	HTTP   *http.Client
	Issuer string
}

func NewRoot(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "connect",
		Short:         "Connect API from the terminal",
		Long:          "connect exposes the Connect messaging API (SMS, email, contacts, reports) to developers and coding agents.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.BoolVar(&app.JSON, "json", false, "print the raw API response as JSON")
	pf.StringVar(&app.Profile, "profile", os.Getenv("CONNECT_PROFILE"), "credential profile (env CONNECT_PROFILE)")
	pf.StringVar(&app.APIURL, "api-url", app.APIURL, "override the API origin (env CONNECT_API_URL)")
	pf.StringVar(&app.Workspace, "workspace", "", "workspace id to act on (API-key credentials only)")
	pf.BoolVar(&app.NoKeyring, "no-keyring", false, "store credentials in a file instead of the OS keyring")
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if app.Profile != "" && creds.ProfileName(app.Profile) != app.Profile {
			return apierr.Usage("--profile (or CONNECT_PROFILE) may only contain lower case letters, digits and hyphens")
		}
		return nil
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return apierr.Usage(err.Error()) })
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	root.SetIn(app.In)
	root.AddCommand(newVersion(app), newDocs(app), newAuth(app), newWorkspace(app), newSms(app), newBusiness(app), newEmail(app), newContacts(app), newLists(app), newSuppressions(app), newReports(app), newBilling(app), newWebhooks(app), newAPIKeys(app), newMCP(app), newListen(app), newInternal(app))
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	app := &App{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	return run(NewRoot(app), app, os.Args[1:])
}

func run(root *cobra.Command, app *App, args []string) int {
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var ae *apierr.Error
		if !errors.As(err, &ae) {
			ae = &apierr.Error{Code: "unexpected", Message: err.Error()}
		}
		ae.Print(app.Out, app.Err, app.JSON)
		return ae.ExitCode()
	}
	return 0
}

// annotate records the operation ids a command covers and the scope it needs.
func annotate(c *cobra.Command, operations, scope string) *cobra.Command {
	c.Annotations = map[string]string{"operation": operations, "scope": scope}
	return c
}

// exactArgs wraps cobra.ExactArgs so argument mistakes exit 2.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return apierr.Usage(err.Error())
		}
		return nil
	}
}
