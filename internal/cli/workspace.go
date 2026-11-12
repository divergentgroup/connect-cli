package cli

import (
	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/spf13/cobra"
)

func newWorkspace(app *App) *cobra.Command {
	c := &cobra.Command{Use: "workspace", Short: "Workspace details"}
	c.AddCommand(annotate(&cobra.Command{
		Use:   "show",
		Short: "Show the current workspace, its mode and whether it can send",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetWorkspace(cmd.Context(), nil)
			return app.finish(resp, err, show(func(w api.WorkspaceDto) {
				mode, sending := "test", "allowed"
				if w.Live {
					mode = "live"
				}
				if w.Sending.Blocked {
					sending = "blocked"
					if w.Sending.Reason != nil {
						sending += " (" + string(*w.Sending.Reason) + ")"
					}
				}
				app.printer().KV([][2]string{{"Workspace", w.Name}, {"ID", w.Id}, {"Mode", mode}, {"Sending", sending}, {"Default sender", str(w.DefaultSender)},
					{"Country", w.Country}, {"Time zone", str(w.TimeZone)}, {"API only mode", yesNo(w.ApiOnlyMode)}})
			}))
		},
	}, "V2GetWorkspace", "any"))
	return c
}
