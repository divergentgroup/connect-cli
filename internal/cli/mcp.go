package cli

import (
	"context"
	"sync"

	"github.com/divergentgroup/connect-cli/internal/auth"
	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/divergentgroup/connect-cli/internal/mcpproxy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMCP(app *App) *cobra.Command {
	var upstream string
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the hosted Connect MCP server on stdio using the stored credential",
		Long: `Runs a local stdio MCP server that mirrors the tools of the hosted Connect MCP server and forwards
every call with the selected profile's credential. Point any MCP client at "connect mcp".`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := app.origin("MCP server", upstream, config.MCPEndpoint); err != nil {
				return err
			}
			cred, err := app.credential(cmd.Context())
			if err != nil {
				return err
			}
			store, err := app.store()
			if err != nil {
				return err
			}
			// Each upstream request refreshes an expiring token, so a long session keeps working.
			var mu sync.Mutex
			fresh := func(ctx context.Context) (*creds.Credential, error) {
				mu.Lock()
				defer mu.Unlock()
				p, err := auth.Refresh(ctx, store, cred.Profile, app.issuer())
				if err != nil {
					return nil, err
				}
				cred.Profile = p
				c := *cred
				return &c, nil
			}
			return mcpproxy.Run(cmd.Context(), upstream, fresh, app.httpClient().Transport, &mcp.StdioTransport{})
		},
	}
	c.Flags().StringVar(&upstream, "upstream", config.MCPEndpoint, "hosted MCP endpoint")
	return c
}
