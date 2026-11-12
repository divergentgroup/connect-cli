package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/output"
	"github.com/spf13/cobra"
)

func newSms(app *App) *cobra.Command {
	c := &cobra.Command{Use: "sms", Short: "Send, cancel, inspect and search SMS"}
	c.AddCommand(newSmsSend(app), newSmsCancel(app), newSmsCancelBatch(app), newSmsGet(app), newSmsEvents(app), newSmsSearch(app), newSmsPreview(app), newSmsBatches(app),
		newSmsSendBulk(app), newSmsSendList(app), newSmsConversations(app), newSmsSimulator(app), newSmsTemplates(app), newSmsSenders(app))
	return c
}

var smsKinds = []api.SmsKind{api.SmsKindTransactional, api.SmsKindMarketing, api.SmsKindAuthentication}

func parseTime(flag, s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, apierr.Usage(fmt.Sprintf("--%s must be RFC 3339, e.g. 2026-09-05T14:00:00Z", flag))
	}
	return &t, nil
}

func parseLink(mode string) (*api.SmsLinkDto, error) {
	m, err := enum("link-mode", mode, api.Unsubscribe, api.Reply, api.Both)
	if m == nil {
		return nil, err
	}
	return &api.SmsLinkDto{Include: ptr(true), Mode: m}, nil
}

func parseTemplateData(s string) (*api.JsonDocument, error) {
	if s == "" {
		return nil, nil
	}
	var v api.JsonDocument
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, apierr.Usage("--data must be a JSON object")
	}
	return &v, nil
}

// template builds the v2 template object from a stored template name and/or merge data.
func template(name string, data *api.JsonDocument) *api.SmsTemplateDto {
	if name == "" && data == nil {
		return nil
	}
	return &api.SmsTemplateDto{Name: optional(name), Data: data}
}

func newSmsSend(app *App) *cobra.Command {
	var to, from, content, tmpl, data, kind, at, ref, tag, unicode, link string
	c := annotate(&cobra.Command{
		Use:   "send",
		Short: "Send one SMS (test recipients need sms.send; anyone else needs sms.send.live)",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if content == "" && tmpl == "" {
				return apierr.Usage("one of --content or --template is required")
			}
			k, err := enum("kind", kind, smsKinds...)
			if err != nil {
				return err
			}
			sendAt, err := parseTime("at", at)
			if err != nil {
				return err
			}
			ml, err := parseLink(link)
			if err != nil {
				return err
			}
			td, err := parseTemplateData(data)
			if err != nil {
				return err
			}
			um, err := enum("unicode", unicode, api.Allow, api.Deny, api.Strip, api.Smart)
			if err != nil {
				return err
			}
			c, cred, err := app.client(cmd)
			if err != nil {
				return err
			}
			app.sendNotice(cred)
			resp, err := c.V2SendSms(cmd.Context(), nil, api.SendSmsV2Request{Sender: from, Recipient: to, Content: optional(content), Template: template(tmpl, td),
				Kind: k, ScheduledAt: sendAt, ClientReference: optional(ref), ClientTag: optional(tag), Unicode: um, Link: ml})
			return app.finish(resp, err, show(func(m api.SmsMessageDto) { fmt.Fprintln(app.Out, m.Id) }))
		},
	}, "V2SendSms", "connect:sms.send")
	f := c.Flags()
	f.StringVar(&to, "to", "", "recipient number in E.164, e.g. +447700900001 (simulator)")
	f.StringVar(&from, "from", "", "sender name or number")
	f.StringVar(&content, "content", "", "message text")
	f.StringVar(&tmpl, "template", "", "stored template name instead of --content")
	f.StringVar(&data, "data", "", "template merge data as a JSON object")
	f.StringVar(&kind, "kind", "transactional", "transactional, marketing or authentication")
	f.StringVar(&at, "at", "", "schedule for an RFC 3339 time (UTC)")
	f.StringVar(&ref, "client-ref", "", "your own reference for this message")
	f.StringVar(&tag, "client-tag", "", "tag for spend reporting")
	f.StringVar(&unicode, "unicode", "", "allow, deny, strip or smart for non-GSM characters")
	f.StringVar(&link, "link-mode", "", "append a message link: unsubscribe, reply or both")
	_ = c.MarkFlagRequired("to")
	_ = c.MarkFlagRequired("from")
	return c
}

func newSmsCancel(app *App) *cobra.Command {
	return annotate(&cobra.Command{
		Use:   "cancel ID",
		Short: "Cancel a message that is still waiting to send",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CancelSms(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Cancelled "+args[0]))
		},
	}, "V2CancelSms", "connect:sms.send")
}

func newSmsCancelBatch(app *App) *cobra.Command {
	return annotate(&cobra.Command{
		Use:   "cancel-batch BATCH",
		Short: "Cancel the messages of a batch that have not been sent yet",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CancelSmsBatch(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(b api.SmsBatchDto) { fmt.Fprintf(app.Out, "Cancelled %d message(s)\n", b.Counts.Cancelled) }))
		},
	}, "V2CancelSmsBatch", "connect:sms.send.live")
}

func printMessage(app *App, m api.SmsMessageDto) {
	reason := ""
	if r := m.Reason; r != nil {
		reason = fmt.Sprintf("%s %s: %s (retryable: %s)", r.Category, r.Code, r.Description, yesNo(r.Retryable))
	}
	kind := ""
	if m.Kind != nil {
		kind = string(*m.Kind)
	}
	app.printer().KV([][2]string{{"ID", m.Id}, {"Direction", string(m.Direction)}, {"Sender", str(m.Sender)}, {"Recipient", str(m.Recipient)}, {"Status", string(m.Status)},
		{"Reason", reason}, {"Kind", kind}, {"Content", str(m.Content)}, {"Parts", fmt.Sprint(m.Parts)}, {"Encoding", string(m.Encoding)}, {"Cost", money(m.Cost)},
		{"Client ref", str(m.ClientReference)}, {"Client tag", str(m.ClientTag)}, {"Batch", str(m.BatchId)}, {"Created", fmtTime(&m.CreatedAt)},
		{"Scheduled", fmtTime(m.ScheduledAt)}, {"Sent", fmtTime(m.SentAt)}, {"Accepted", fmtTime(m.AcceptedAt)}, {"Delivered", fmtTime(m.DeliveredAt)},
		{"Failed", fmtTime(m.FailedAt)}, {"Received", fmtTime(m.ReceivedAt)}, {"Expires", fmtTime(m.ExpiresAt)}})
}

func newSmsGet(app *App) *cobra.Command {
	return annotate(&cobra.Command{
		Use:   "get ID",
		Short: "Show a message, outbound or inbound",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetSms(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(m api.SmsMessageDto) { printMessage(app, m) }))
		},
	}, "V2GetSms", "connect:sms.read")
}

func newSmsEvents(app *App) *cobra.Command {
	var p paging
	c := annotate(&cobra.Command{
		Use:   "events ID",
		Short: "List a message's events from the last seven days, oldest first",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetMessageEvents(cmd.Context(), args[0], &api.V2GetMessageEventsParams{Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"WHEN", "EVENT", "ID"}, func(e api.V2EventDto) []string { return []string{fmtTime(&e.OccurredAt), e.Type, e.Id} }))
		},
	}, "V2GetMessageEvents", "connect:sms.read")
	p.bind(c)
	return c
}

func messageRow(m api.SmsMessageDto) []string {
	return []string{m.Id, fmtTime(&m.CreatedAt), string(m.Direction), str(m.Sender), str(m.Recipient), string(m.Status), clip(m.Content)}
}

var messageHeaders = []string{"ID", "CREATED", "DIR", "FROM", "TO", "STATUS", "CONTENT"}

func newSmsSearch(app *App) *cobra.Command {
	var direction, status, number, sender, recipient, batch, ref, tag, inbound, since, until string
	var p paging
	c := annotate(&cobra.Command{
		Use:   "search",
		Short: "List messages, newest first, with filters",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, err := parseTime("since", since)
			if err != nil {
				return err
			}
			to, err := parseTime("until", until)
			if err != nil {
				return err
			}
			d, err := enum("direction", direction, api.Outbound, api.Inbound)
			if err != nil {
				return err
			}
			st, err := enum("status", status, api.SmsMessageStatusQueued, api.SmsMessageStatusSent, api.SmsMessageStatusDelivered, api.SmsMessageStatusFailed, api.SmsMessageStatusCancelled, api.SmsMessageStatusReceived)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListSms(cmd.Context(), &api.V2ListSmsParams{Direction: d, Status: st, ConversationWith: optional(number), Sender: optional(sender), Recipient: optional(recipient),
				BatchId: optional(batch), ClientReference: optional(ref), ClientTag: optional(tag), InboundNumber: optional(inbound), Since: from, Until: to, Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, messageHeaders, messageRow))
		},
	}, "V2ListSms", "connect:sms.read")
	f := c.Flags()
	f.StringVar(&direction, "direction", "", "outbound or inbound")
	f.StringVar(&status, "status", "", "queued, sent, delivered, failed, cancelled or received")
	f.StringVar(&number, "number", "", "messages to or from this number (E.164)")
	f.StringVar(&sender, "from", "", "messages from this sender")
	f.StringVar(&recipient, "to", "", "messages to this recipient (E.164)")
	f.StringVar(&batch, "batch", "", "messages in this batch")
	f.StringVar(&ref, "client-ref", "", "messages with this client reference")
	f.StringVar(&tag, "client-tag", "", "messages with this client tag")
	f.StringVar(&inbound, "inbound-number", "", "messages on this virtual number of yours (E.164)")
	f.StringVar(&since, "since", "", "created at or after this RFC 3339 time")
	f.StringVar(&until, "until", "", "created at or before this RFC 3339 time")
	p.bind(c)
	return c
}

func newSmsPreview(app *App) *cobra.Command {
	var content, to string
	c := annotate(&cobra.Command{
		Use:   "preview",
		Short: "Count parts and encoding, and estimate cost for a recipient",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2PreviewSms(cmd.Context(), nil, api.PreviewSmsV2Request{Content: content, Recipient: optional(to)})
			return app.finish(resp, err, show(func(r api.SmsPreviewDto) {
				cost := money(r.Cost)
				if cost == "" && to != "" {
					cost = "unknown for this destination"
				}
				app.printer().KV([][2]string{{"Encoding", string(r.Encoding)}, {"Parts", fmt.Sprint(r.Parts)}, {"Estimated cost", cost}})
			}))
		},
	}, "V2PreviewSms", "connect:sms.read")
	c.Flags().StringVar(&content, "content", "", "message text")
	c.Flags().StringVar(&to, "to", "", "recipient number, for the cost estimate")
	_ = c.MarkFlagRequired("content")
	return c
}

func printBatch(app *App, b api.SmsBatchDto) {
	n := b.Counts
	paced := ""
	if b.Paced != nil {
		paced = fmt.Sprintf("%d per %s", b.Paced.RatePerPeriod, b.Paced.Period)
	}
	app.printer().KV([][2]string{{"ID", b.Id}, {"Status", string(b.Status)}, {"Source", string(b.Source)}, {"Client tag", str(b.ClientTag)}, {"Created", fmtTime(&b.CreatedAt)},
		{"Scheduled", fmtTime(b.ScheduledAt)}, {"Paced", paced}, {"Total", fmt.Sprint(n.Total)},
		{"Counts", fmt.Sprintf("queued %d, sent %d, delivered %d, failed %d, skipped %d, cancelled %d", n.Queued, n.Sent, n.Delivered, n.Failed, n.Skipped, n.Cancelled)},
		{"Skipped", fmt.Sprintf("suppressed %d, recently messaged %d, invalid %d", b.Skipped.Suppressed, b.Skipped.RecentlyMessaged, b.Skipped.Invalid)}})
	for _, e := range b.Errors {
		fmt.Fprintf(app.Err, "row %d: %s\n", e.Index+1, output.Clean(e.Detail))
	}
}

func newSmsBatches(app *App) *cobra.Command {
	c := &cobra.Command{Use: "batches", Short: "Bulk and list send batches"}
	var tag, source string
	var p paging
	list := annotate(&cobra.Command{
		Use:   "list",
		Short: "List batches, newest first",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			src, err := enum("source", source, api.SmsBatchSourceApi, api.SmsBatchSourceImport, api.SmsBatchSourceList)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListSmsBatches(cmd.Context(), &api.V2ListSmsBatchesParams{ClientTag: optional(tag), Source: src, Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"ID", "CREATED", "SOURCE", "STATUS", "TOTAL", "DELIVERED", "FAILED"}, func(b api.SmsBatchDto) []string {
				return []string{b.Id, fmtTime(&b.CreatedAt), string(b.Source), string(b.Status), fmt.Sprint(b.Counts.Total), fmt.Sprint(b.Counts.Delivered), fmt.Sprint(b.Counts.Failed)}
			}))
		},
	}, "V2ListSmsBatches", "connect:sms.read")
	list.Flags().StringVar(&tag, "client-tag", "", "only batches with this client tag")
	list.Flags().StringVar(&source, "source", "", "api, import or list")
	p.bind(list)
	get := annotate(&cobra.Command{
		Use:   "get ID",
		Short: "Show a batch with its counts (its messages: sms search --batch ID)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetSmsBatch(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(b api.SmsBatchDto) { printBatch(app, b) }))
		},
	}, "V2GetSmsBatch", "connect:sms.read")
	c.AddCommand(list, get)
	return c
}
