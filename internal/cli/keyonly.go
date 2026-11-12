package cli

import (
	"fmt"
	"strings"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/spf13/cobra"
)

// secretOnce prints a newly issued secret exactly once, with a warning, in human mode.
func (app *App) secretOnce(label string, value *string) {
	if value == nil {
		return
	}
	fmt.Fprintf(app.Err, "%s is shown once and cannot be retrieved later; store it now.\n", label)
	fmt.Fprintln(app.Out, *value)
}

func newWebhooks(app *App) *cobra.Command {
	c := &cobra.Command{Use: "webhooks", Short: "Webhook endpoints and deliveries (API key only)"}
	list := annotate(&cobra.Command{
		Use: "list", Short: "List webhook endpoints", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListWebhooks(cmd.Context(), nil)
			return app.finish(resp, err, listOf(app, []string{"ID", "URL", "ACTIVE", "EVENTS", "INBOUND NUMBER", "DESCRIPTION"}, func(w api.WebhookDto) []string {
				return []string{w.Id, w.Url, yesNo(w.Active), strings.Join(w.Events, ","), str(w.InboundNumber), str(w.Description)}
			}))
		},
	}, "V2ListWebhooks", scopeAPIKeyOnly)
	get := annotate(&cobra.Command{
		Use: "get ID", Short: "Show a webhook's settings and health", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetWebhook(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(w api.WebhookDto) {
				app.printer().KV([][2]string{{"ID", w.Id}, {"URL", w.Url}, {"Active", yesNo(w.Active)}, {"Auto-disabled", yesNo(w.AutoDisabled)}, {"Events", strings.Join(w.Events, ",")},
					{"Inbound number", str(w.InboundNumber)}, {"Description", str(w.Description)}, {"API version", str(w.ApiVersion)}, {"Secret version", fmt.Sprint(w.SecretVersion)},
					{"Consecutive failures", fmt.Sprint(w.ConsecutiveFailures)}, {"Failing since", fmtTime(w.FailingSince)}, {"Auto-disable exempt", yesNo(w.AutoDisableExempt)}})
			}))
		},
	}, "V2GetWebhook", scopeAPIKeyOnly)
	var url, desc, inbound string
	var events []string
	create := annotate(&cobra.Command{
		Use: "create", Short: "Create a webhook endpoint; the signing secret is printed once", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateWebhook(cmd.Context(), nil, api.CreateWebhookRequest{Url: url, Events: events, Description: optional(desc), InboundNumber: optional(inbound)})
			return app.finish(resp, err, show(func(w api.WebhookDto) {
				fmt.Fprintln(app.Out, w.Id)
				app.secretOnce("The webhook secret", w.Secret)
			}))
		},
	}, "V2CreateWebhook", scopeAPIKeyOnly)
	create.Flags().StringVar(&url, "url", "", "HTTPS endpoint to call")
	create.Flags().StringArrayVar(&events, "event", nil, "event type to deliver, repeatable (sms.delivered, sms.received, ...)")
	create.Flags().StringVar(&desc, "description", "", "description")
	create.Flags().StringVar(&inbound, "inbound-number", "", "only events for this inbound number (E.164)")
	_ = create.MarkFlagRequired("url")
	_ = create.MarkFlagRequired("event")

	var active, inactive, exempt bool
	var newURL, newDesc string
	var newEvents []string
	configure := annotate(&cobra.Command{
		Use: "configure ID", Short: "Change a webhook (only given flags change)", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if active && inactive {
				return apierr.Usage("give at most one of --active or --inactive")
			}
			body := api.UpdateWebhookRequest{Url: optional(newURL), Description: optional(newDesc), Events: sliceOrNil(newEvents)}
			if active || inactive {
				body.Active = &active
			}
			if cmd.Flags().Changed("auto-disable-exempt") {
				body.AutoDisableExempt = &exempt
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2UpdateWebhook(cmd.Context(), args[0], nil, body)
			return app.finish(resp, err, show(func(w api.WebhookDto) {
				fmt.Fprintln(app.Out, "Webhook updated")
				app.secretOnce("The webhook secret", w.Secret)
			}))
		},
	}, "V2UpdateWebhook", scopeAPIKeyOnly)
	configure.Flags().BoolVar(&active, "active", false, "enable the endpoint")
	configure.Flags().BoolVar(&inactive, "inactive", false, "disable the endpoint")
	configure.Flags().StringArrayVar(&newEvents, "event", nil, "event type to deliver, repeatable; replaces the current set")
	configure.Flags().StringVar(&newURL, "url", "", "new HTTPS endpoint")
	configure.Flags().StringVar(&newDesc, "description", "", "new description")
	configure.Flags().BoolVar(&exempt, "auto-disable-exempt", false, "never auto-disable after repeated failures (=false to undo)")

	rotate := annotate(&cobra.Command{
		Use: "rotate-secret ID", Short: "Issue a new signing secret; printed once", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2RotateWebhookSecret(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(w api.WebhookDto) { app.secretOnce("The new webhook secret", w.Secret) }))
		},
	}, "V2RotateWebhookSecret", scopeAPIKeyOnly)
	del := annotate(&cobra.Command{
		Use: "delete ID", Short: "Delete a webhook endpoint", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteWebhook(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Webhook deleted"))
		},
	}, "V2DeleteWebhook", scopeAPIKeyOnly)

	var p paging
	logs := annotate(&cobra.Command{
		Use: "logs ID", Aliases: []string{"deliveries"}, Short: "List a webhook's event deliveries, newest first", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListWebhookDeliveries(cmd.Context(), args[0], &api.V2ListWebhookDeliveriesParams{Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"CREATED", "EVENT", "STATUS", "ATTEMPTS", "LAST CODE", "ERROR"}, func(d api.WebhookDeliveryDto) []string {
				code := ""
				if d.LastStatusCode != nil {
					code = fmt.Sprint(*d.LastStatusCode)
				}
				return []string{fmtTime(&d.CreatedAt), str(d.EventType), string(d.Status), fmt.Sprint(d.Attempts), code, str(d.LastError)}
			}))
		},
	}, "V2ListWebhookDeliveries", scopeAPIKeyOnly)
	p.bind(logs)
	c.AddCommand(list, get, create, configure, rotate, del, logs)
	return c
}

func newAPIKeys(app *App) *cobra.Command {
	c := &cobra.Command{Use: "api-keys", Short: "Workspace API keys (API key only)"}
	list := annotate(&cobra.Command{
		Use: "list", Short: "List API keys (values are never shown)", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListApiKeys(cmd.Context(), nil)
			return app.finish(resp, err, listOf(app, []string{"ID", "NAME", "KEY", "ACTIVE FROM", "EXPIRES", "LAST USED", "STRICT"}, func(k api.ApiKeyDto) []string {
				return []string{k.Id, str(k.Name), k.KeyPrefix + "..." + k.KeyLast4, fmtTime(&k.ActiveFrom), fmtTime(k.ExpiresAt), fmtTime(k.LastUsedAt), yesNo(k.StrictMode)}
			}))
		},
	}, "V2ListApiKeys", scopeAPIKeyOnly)
	var name string
	create := annotate(&cobra.Command{
		Use: "create", Short: "Create an API key; the key is printed once", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateApiKey(cmd.Context(), nil, api.CreateApiKeyRequest{Name: optional(name)})
			return app.finish(resp, err, show(func(k api.ApiKeyDto) {
				fmt.Fprintln(app.Out, k.Id)
				app.secretOnce("The API key", k.Key)
			}))
		},
	}, "V2CreateApiKey", scopeAPIKeyOnly)
	create.Flags().StringVar(&name, "name", "", "a name to recognise the key by")
	cycle := annotate(&cobra.Command{
		Use: "cycle ID", Aliases: []string{"rotate"}, Short: "Issue a replacement key; the old one expires an hour later. Printed once", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2RotateApiKey(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(k api.ApiKeyDto) {
				fmt.Fprintln(app.Out, k.Id)
				app.secretOnce("The new API key", k.Key)
				app.secretOnce("The new signing secret", k.SigningSecret)
			}))
		},
	}, "V2RotateApiKey", scopeAPIKeyOnly)
	del := annotate(&cobra.Command{
		Use: "delete ID", Short: "Revoke an API key", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteApiKey(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("API key revoked"))
		},
	}, "V2DeleteApiKey", scopeAPIKeyOnly)
	c.AddCommand(list, create, cycle, del)
	return c
}
