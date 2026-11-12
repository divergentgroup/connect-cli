package cli

import (
	"fmt"
	"strings"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/spf13/cobra"
)

func newSmsConversations(app *App) *cobra.Command {
	c := &cobra.Command{Use: "conversations", Short: "Two-way SMS threads on your inbound numbers"}
	var inbound string
	var inboundOnly bool
	var lp paging
	list := annotate(&cobra.Command{
		Use: "list", Short: "List conversations, most recent activity first", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			var hi *bool
			if inboundOnly {
				hi = ptr(true)
			}
			resp, err := c.V2ListConversations(cmd.Context(), &api.V2ListConversationsParams{InboundNumber: optional(inbound), HasInbound: hi, Limit: optional(lp.limit), Cursor: optional(lp.cursor)})
			return app.finish(resp, err, listOf(app, []string{"INBOUND NUMBER", "COUNTERPARTY", "CONTACT", "LAST", "DIRECTION", "MESSAGES", "PREVIEW"}, func(v api.ConversationDto) []string {
				return []string{v.InboundNumber, str(v.Counterparty), str(v.ContactName), fmtTime(&v.LastMessageAt), string(v.LastDirection), fmt.Sprint(v.MessageCount), clip(v.LastMessage)}
			}))
		},
	}, "V2ListConversations", "connect:sms.read")
	list.Flags().StringVar(&inbound, "inbound-number", "", "only conversations on this virtual number (E.164)")
	list.Flags().BoolVar(&inboundOnly, "inbound-only", false, "only threads where the contact has replied")
	lp.bind(list)

	var before string
	var gp paging
	get := annotate(&cobra.Command{
		Use: "get VMN COUNTERPARTY", Short: "Show the messages in a conversation, newest first", Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := parseTime("before", before)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListSms(cmd.Context(), &api.V2ListSmsParams{InboundNumber: &args[0], ConversationWith: &args[1], Until: b, Limit: optional(gp.limit), Cursor: optional(gp.cursor)})
			return app.finish(resp, err, listOf(app, messageHeaders, messageRow))
		},
	}, "V2ListSms", "connect:sms.read")
	get.Flags().StringVar(&before, "before", "", "only messages created at or before this RFC 3339 time")
	gp.bind(get)

	var content string
	reply := annotate(&cobra.Command{
		Use: "reply VMN COUNTERPARTY", Short: "Reply in a conversation from the inbound number", Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cred, err := app.client(cmd)
			if err != nil {
				return err
			}
			app.sendNotice(cred)
			resp, err := c.V2SendSms(cmd.Context(), nil, api.SendSmsV2Request{Sender: args[0], Recipient: args[1], Content: &content, Kind: ptr(api.SmsKindTransactional)})
			return app.finish(resp, err, show(func(m api.SmsMessageDto) { fmt.Fprintln(app.Out, m.Id) }))
		},
	}, "V2SendSms", "connect:sms.send")
	reply.Flags().StringVar(&content, "content", "", "message text")
	_ = reply.MarkFlagRequired("content")
	c.AddCommand(list, get, reply)
	return c
}

func newSmsSimulator(app *App) *cobra.Command {
	c := &cobra.Command{Use: "simulator", Short: "Inspect the simulator handsets of a test workspace"}
	c.AddCommand(annotate(&cobra.Command{
		Use: "phones", Short: "List simulated handsets and their recent messages", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListSimulatorPhones(cmd.Context(), nil)
			return app.finish(resp, err, show(func(r listPage[api.SimulatorPhoneDto]) {
				var rows [][]string
				for _, p := range r.Data {
					rows = append(rows, []string{p.Number, p.Name, p.Behaviour, "", "", ""})
					for _, m := range p.Messages {
						rows = append(rows, []string{"", "", "", fmtTime(&m.CreatedAt), string(m.Direction) + " " + string(m.Status), m.Content})
					}
				}
				app.printer().Table([]string{"NUMBER", "NAME", "BEHAVIOUR", "AT", "STATUS", "CONTENT"}, rows)
			}))
		},
	}, "V2ListSimulatorPhones", "connect:sms.read"), annotate(&cobra.Command{
		Use: "clear", Short: "Delete your messages to and from the simulator handsets", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ClearSimulatorPhones(cmd.Context(), nil)
			return app.finish(resp, err, app.done("Simulator inboxes cleared"))
		},
	}, "V2ClearSimulatorPhones", "connect:sms.send"))
	return c
}

func newSmsTemplates(app *App) *cobra.Command {
	c := &cobra.Command{Use: "templates", Short: "Message templates (reads any credential; writes need an API key)"}
	var name, content, desc, sample string
	var p paging
	list := annotate(&cobra.Command{
		Use: "list", Short: "List templates", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListTemplates(cmd.Context(), &api.V2ListTemplatesParams{Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"ID", "NAME", "DESCRIPTION", "UPDATED"}, func(t api.TemplateDto) []string {
				return []string{t.Id, t.Name, str(t.Description), fmtTime(&t.UpdatedAt)}
			}))
		},
	}, "V2ListTemplates", "connect:sms.read")
	p.bind(list)
	get := annotate(&cobra.Command{
		Use: "get ID", Short: "Show a template", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetTemplate(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(t api.TemplateDto) {
				app.printer().KV([][2]string{{"ID", t.Id}, {"Name", t.Name}, {"Description", str(t.Description)}, {"Content", t.Content}, {"Updated", fmtTime(&t.UpdatedAt)}})
			}))
		},
	}, "V2GetTemplate", "connect:sms.read")
	create := annotate(&cobra.Command{
		Use: "create", Short: "Create a template", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			sd, err := parseTemplateData(sample)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateTemplate(cmd.Context(), nil, api.CreateTemplateRequest{Name: name, Content: content, Description: optional(desc), SampleData: sd})
			return app.finish(resp, err, show(func(t api.TemplateDto) { fmt.Fprintln(app.Out, t.Id) }))
		},
	}, "V2CreateTemplate", scopeAPIKeyOnly)
	update := annotate(&cobra.Command{
		Use: "update ID", Short: "Change a template (only given flags change)", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sd, err := parseTemplateData(sample)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2UpdateTemplate(cmd.Context(), args[0], nil, api.UpdateTemplateRequest{Name: optional(name), Content: optional(content), Description: optional(desc), SampleData: sd})
			return app.finish(resp, err, app.done("Template updated"))
		},
	}, "V2UpdateTemplate", scopeAPIKeyOnly)
	for _, w := range []*cobra.Command{create, update} {
		w.Flags().StringVar(&name, "name", "", "template name")
		w.Flags().StringVar(&content, "content", "", "template body (Liquid)")
		w.Flags().StringVar(&desc, "description", "", "description")
		w.Flags().StringVar(&sample, "sample-data", "", "sample data as a JSON object")
	}
	_ = create.MarkFlagRequired("name")
	_ = create.MarkFlagRequired("content")
	del := annotate(&cobra.Command{
		Use: "delete ID", Short: "Delete a template", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteTemplate(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Template deleted"))
		},
	}, "V2DeleteTemplate", scopeAPIKeyOnly)
	c.AddCommand(list, get, create, update, del)
	return c
}

func newSmsSenders(app *App) *cobra.Command {
	c := &cobra.Command{Use: "senders", Short: "Sender names and inbound numbers"}
	var p paging
	list := annotate(&cobra.Command{
		Use: "list", Short: "List senders and their approval state", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListSenders(cmd.Context(), &api.V2ListSendersParams{Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"ID", "NAME", "KIND", "STATUS", "DEFAULT", "COUNTRIES"}, func(s api.SenderDto) []string {
				cs := make([]string, len(s.Countries))
				for i, a := range s.Countries {
					cs[i] = a.Country + ":" + string(a.Status)
				}
				return []string{s.Id, s.Name, string(s.Kind), string(s.Status), yesNo(s.Default), strings.Join(cs, " ")}
			}))
		},
	}, "V2ListSenders", "connect:sms.senders.read")
	p.bind(list)
	check := annotate(&cobra.Command{
		Use: "check NAME", Short: "Check whether a sender name could be requested", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CheckSenderAvailability(cmd.Context(), &api.V2CheckSenderAvailabilityParams{Name: args[0]})
			return app.finish(resp, err, show(func(r api.SenderAvailabilityDto) {
				app.printer().KV([][2]string{{"Name", r.Name}, {"Available", yesNo(r.Available)}, {"Needs number verification", yesNo(r.RequiresVerification)}, {"Reason", str(r.Reason)}})
			}))
		},
	}, "V2CheckSenderAvailability", "connect:sms.senders.read")
	var sample string
	var countries []string
	request := annotate(&cobra.Command{
		Use:   "request NAME",
		Short: "Request a sender name (a UK mobile starting 07 is texted a code: then run `senders verify`)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateSender(cmd.Context(), nil, api.CreateSenderRequest{Name: args[0], SampleContent: sample, Countries: sliceOrNil(countries)})
			return app.finish(resp, err, show(func(s api.SenderDto) {
				app.printer().KV([][2]string{{"ID", s.Id}, {"Name", s.Name}, {"Status", string(s.Status)}})
			}))
		},
	}, "V2CreateSender", "connect:sms.senders.write")
	request.Flags().StringVar(&sample, "sample", "", "sample message content for review (20 to 256 characters)")
	request.Flags().StringArrayVar(&countries, "country", nil, "ISO country code, repeatable (default: the workspace's country)")
	_ = request.MarkFlagRequired("sample")
	verify := annotate(&cobra.Command{
		Use: "verify ID CODE", Short: "Confirm a mobile number sender with the code texted to it", Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2VerifySender(cmd.Context(), args[0], nil, api.VerifySenderRequest{Code: args[1]})
			return app.finish(resp, err, app.done("Sender verified"))
		},
	}, "V2VerifySender", "connect:sms.senders.write")
	cancel := annotate(&cobra.Command{
		Use: "cancel ID", Short: "Withdraw a sender request that has not been approved", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteSender(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Sender request cancelled"))
		},
	}, "V2DeleteSender", scopeAPIKeyOnly)
	c.AddCommand(list, check, request, verify, cancel)
	return c
}
