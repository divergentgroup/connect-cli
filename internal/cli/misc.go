package cli

import (
	"fmt"

	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/skilldoc"
	"github.com/spf13/cobra"
)

func newVersion(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the connect version",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if app.JSON {
				_, err := fmt.Fprintf(app.Out, "{\"version\":%q}\n", config.Version)
				return err
			}
			_, err := fmt.Fprintf(app.Out, "connect %s\n", config.Version)
			return err
		},
	}
}

func newDocs(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Open the CLI documentation",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(app.Out, config.DocsURL)
			_ = openBrowser(config.DocsURL)
			return nil
		},
	}
}

func newInternal(app *App) *cobra.Command {
	c := &cobra.Command{Use: "internal", Short: "Maintainer commands", Hidden: true}
	var out string
	sd := &cobra.Command{
		Use:   "skill-doc",
		Short: "Write SKILL.md and reference/commands.md from the command tree",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := skilldoc.Write(cmd.Root(), out); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Wrote %s/SKILL.md and %s/reference/commands.md\n", out, out)
			return nil
		},
	}
	sd.Flags().StringVar(&out, "out", ".", "output directory")
	c.AddCommand(sd)
	return c
}
