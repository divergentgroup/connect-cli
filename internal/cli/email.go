package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/output"
	"github.com/spf13/cobra"
)

const (
	scopeEmailRead = "connect:email.read"
	scopeEmailSend = "connect:email.send"
)

// bodyFlag binds --NAME and --NAME-file, so a long body can come from a file.
type bodyFlag struct{ name, text, file string }

func (b *bodyFlag) bind(c *cobra.Command, usage string) {
	c.Flags().StringVar(&b.text, b.name, "", usage)
	c.Flags().StringVar(&b.file, b.name+"-file", "", usage+", read from this file")
}

// value is the body given, or nil when neither flag was.
func (b *bodyFlag) value() (*string, error) {
	if b.text != "" && b.file != "" {
		return nil, apierr.Usage(fmt.Sprintf("give --%s or --%s-file, not both", b.name, b.name))
	}
	if b.file == "" {
		return optional(b.text), nil
	}
	raw, err := os.ReadFile(b.file)
	if err != nil {
		return nil, apierr.Usage(err.Error())
	}
	return ptr(string(raw)), nil
}

// jsonObject parses a JSON object flag; nil (not a typed nil) when empty, so it stays off the wire.
func jsonObject(flag, s string) (any, error) {
	if s == "" {
		return nil, nil
	}
	var v map[string]any
	if json.Unmarshal([]byte(s), &v) != nil {
		return nil, apierr.Usage("--" + flag + " must be a JSON object")
	}
	return v, nil
}

func attachments(paths []string) (*[]api.EmailAttachmentDto, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]api.EmailAttachmentDto, len(paths))
	for i, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, apierr.Usage(err.Error())
		}
		ct := mime.TypeByExtension(filepath.Ext(p))
		if ct == "" {
			ct = "application/octet-stream"
		}
		out[i] = api.EmailAttachmentDto{Filename: filepath.Base(p), ContentType: ct, Content: base64.StdEncoding.EncodeToString(raw)}
	}
	return &out, nil
}

var emailFormats = []api.EmailFormat{api.Html, api.Mjml}

func newEmail(app *App) *cobra.Command {
	c := &cobra.Command{Use: "email", Short: "Send, cancel and inspect email; templates, sending domains and test recipients"}
	c.AddCommand(newEmailSend(app), newEmailGet(app), newEmailList(app), newEmailCancel(app), newEmailEvents(app),
		newEmailTemplates(app), newEmailDomains(app), newEmailIncludes(app), newEmailTestRecipients(app))
	return c
}

func newEmailSend(app *App) *cobra.Command {
	var to, from, subject, tmpl, data, format, kind, at, ref, tag, key string
	var files []string
	var track bool
	html, text := bodyFlag{name: "html"}, bodyFlag{name: "text"}
	c := annotate(&cobra.Command{
		Use:   "send",
		Short: "Send one email (test recipients need email.send; anyone else needs email.send.live)",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			h, err := html.value()
			if err != nil {
				return err
			}
			t, err := text.value()
			if err != nil {
				return err
			}
			if h == nil && t == nil && tmpl == "" {
				return apierr.Usage("one of --html, --text or --template is required")
			}
			var content *api.EmailContentRequestDto
			if h != nil || t != nil {
				content = &api.EmailContentRequestDto{Html: h, Text: t}
			}
			td, err := parseTemplateData(data)
			if err != nil {
				return err
			}
			var tref *api.EmailTemplateRefDto
			if tmpl != "" || td != nil {
				tref = &api.EmailTemplateRefDto{Name: optional(tmpl), Data: td}
			}
			f, err := enum("format", format, emailFormats...)
			if err != nil {
				return err
			}
			k, err := enum("kind", kind, api.EmailKindTransactional, api.EmailKindMarketing, api.EmailKindAuthentication)
			if err != nil {
				return err
			}
			sendAt, err := parseTime("at", at)
			if err != nil {
				return err
			}
			att, err := attachments(files)
			if err != nil {
				return err
			}
			c, cred, err := app.client(cmd)
			if err != nil {
				return err
			}
			app.sendNotice(cred)
			resp, err := c.V2SendEmail(cmd.Context(), &api.V2SendEmailParams{IdempotencyKey: optional(key)}, api.SendEmailV2Request{Sender: from, Recipient: to, Subject: subject,
				Content: content, Template: tref, Format: f, Kind: k, ScheduledAt: sendAt, ClientReference: optional(ref), ClientTag: optional(tag), TrackOpens: optional(track), Attachments: att})
			return app.finish(resp, err, show(func(m api.EmailMessageDto) { fmt.Fprintln(app.Out, m.Id) }))
		},
	}, "V2SendEmail", scopeEmailSend)
	f := c.Flags()
	f.StringVar(&to, "to", "", "recipient address")
	f.StringVar(&from, "from", "", "sender address on one of your verified domains, or the shared test sender")
	f.StringVar(&subject, "subject", "", "subject line")
	html.bind(c, "HTML body (MJML with --format mjml)")
	text.bind(c, "plain-text body")
	f.StringVar(&tmpl, "template", "", "stored email template name instead of --html/--text")
	f.StringVar(&data, "data", "", "template merge data as a JSON object")
	f.StringVar(&format, "format", "", "html or mjml")
	f.StringVar(&kind, "kind", "transactional", "transactional, marketing or authentication")
	f.StringVar(&at, "at", "", "schedule for an RFC 3339 time (UTC)")
	f.StringVar(&ref, "client-ref", "", "your own reference for this email")
	f.StringVar(&tag, "client-tag", "", "tag for usage reporting")
	f.BoolVar(&track, "track-opens", false, "track when the email is opened")
	f.StringArrayVar(&files, "attach", nil, "file to attach, repeatable")
	f.StringVar(&key, "idempotency-key", "", "key so a retry does not double-send")
	_ = c.MarkFlagRequired("to")
	_ = c.MarkFlagRequired("from")
	_ = c.MarkFlagRequired("subject")
	return c
}

func newEmailGet(app *App) *cobra.Command {
	return annotate(&cobra.Command{
		Use:   "get ID",
		Short: "Show an email",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetEmail(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(m api.EmailMessageDto) {
				reason, excerpt := "", ""
				if r := m.Reason; r != nil {
					reason = fmt.Sprintf("%s %s: %s (retryable: %s)", r.Category, r.Code, str(r.Description), yesNo(r.Retryable))
				}
				if m.Content != nil {
					excerpt = str(m.Content.Text)
				}
				app.printer().KV([][2]string{{"ID", m.Id}, {"Sender", m.Sender}, {"Recipient", str(m.Recipient)}, {"Subject", str(m.Subject)}, {"Status", string(m.Status)},
					{"Reason", reason}, {"Kind", string(m.Kind)}, {"Content", excerpt}, {"Opens", fmt.Sprint(m.OpenCount)}, {"Client ref", str(m.ClientReference)},
					{"Client tag", str(m.ClientTag)}, {"Created", fmtTime(&m.CreatedAt)}, {"Scheduled", fmtTime(m.ScheduledAt)}, {"Sent", fmtTime(m.SentAt)},
					{"Delivered", fmtTime(m.DeliveredAt)}, {"First opened", fmtTime(m.OpenedAt)}})
			}))
		},
	}, "V2GetEmail", scopeEmailRead)
}

func newEmailList(app *App) *cobra.Command {
	var status, kind, to, ref, tag, since, until string
	var p paging
	c := annotate(&cobra.Command{
		Use:   "list",
		Short: "List emails, newest first, with filters",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, err := parseTime("since", since)
			if err != nil {
				return err
			}
			u, err := parseTime("until", until)
			if err != nil {
				return err
			}
			st, err := enum("status", status, api.EmailMessageStatusQueued, api.EmailMessageStatusSent, api.EmailMessageStatusDelivered, api.EmailMessageStatusFailed, api.EmailMessageStatusCancelled)
			if err != nil {
				return err
			}
			k, err := enum("kind", kind, api.EmailKindTransactional, api.EmailKindMarketing, api.EmailKindAuthentication)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListEmail(cmd.Context(), &api.V2ListEmailParams{Status: st, Kind: k, Recipient: optional(to), ClientReference: optional(ref), ClientTag: optional(tag),
				Since: from, Until: u, Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"ID", "CREATED", "FROM", "TO", "STATUS", "SUBJECT"}, func(m api.EmailMessageDto) []string {
				return []string{m.Id, fmtTime(&m.CreatedAt), m.Sender, str(m.Recipient), string(m.Status), clip(m.Subject)}
			}))
		},
	}, "V2ListEmail", scopeEmailRead)
	f := c.Flags()
	f.StringVar(&status, "status", "", "queued, sent, delivered, failed or cancelled")
	f.StringVar(&kind, "kind", "", "transactional, marketing or authentication")
	f.StringVar(&to, "to", "", "emails to this address")
	f.StringVar(&ref, "client-ref", "", "emails with this client reference")
	f.StringVar(&tag, "client-tag", "", "emails with this client tag")
	f.StringVar(&since, "since", "", "created at or after this RFC 3339 time")
	f.StringVar(&until, "until", "", "created at or before this RFC 3339 time")
	p.bind(c)
	return c
}

func newEmailCancel(app *App) *cobra.Command {
	return annotate(&cobra.Command{
		Use:   "cancel ID",
		Short: "Cancel a scheduled email that is still waiting to send",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CancelEmail(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Cancelled "+args[0]))
		},
	}, "V2CancelEmail", scopeEmailSend)
}

func newEmailEvents(app *App) *cobra.Command {
	var p paging
	c := annotate(&cobra.Command{
		Use:   "events ID",
		Short: "List an email's events, oldest first",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetEmailEvents(cmd.Context(), args[0], &api.V2GetEmailEventsParams{Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"WHEN", "EVENT", "ID"}, func(e api.V2EventDto) []string { return []string{fmtTime(&e.OccurredAt), e.Type, e.Id} }))
		},
	}, "V2GetEmailEvents", scopeEmailRead)
	p.bind(c)
	return c
}

// printBodies prints HTML and text bodies after a heading each, leaving out empty ones.
func (app *App) printBodies(html, text string) {
	for _, b := range [][2]string{{"HTML", html}, {"Text", text}} {
		if b[1] != "" {
			fmt.Fprintf(app.Out, "\n%s:\n%s\n", b[0], output.CleanText(b[1]))
		}
	}
}

func newEmailTemplates(app *App) *cobra.Command {
	c := &cobra.Command{Use: "templates", Short: "Email templates (reads any credential; writes need an API key)"}
	var p paging
	list := annotate(&cobra.Command{
		Use: "list", Short: "List email templates", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListEmailTemplates(cmd.Context(), &api.V2ListEmailTemplatesParams{Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"ID", "NAME", "FORMAT", "DESCRIPTION", "UPDATED"}, func(t api.EmailTemplateDto) []string {
				return []string{t.Id, t.Name, string(t.Format), str(t.Description), fmtTime(&t.UpdatedAt)}
			}))
		},
	}, "V2ListEmailTemplates", scopeEmailRead)
	p.bind(list)
	get := annotate(&cobra.Command{
		Use: "get ID", Short: "Show an email template with its source", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetEmailTemplate(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(t api.EmailTemplateDto) {
				app.printer().KV([][2]string{{"ID", t.Id}, {"Name", t.Name}, {"Format", string(t.Format)}, {"Description", str(t.Description)}, {"Updated", fmtTime(&t.UpdatedAt)}})
				app.printBodies(t.Html, t.Text)
			}))
		},
	}, "V2GetEmailTemplate", scopeEmailRead)

	var name, desc, format, sample string
	html, text := bodyFlag{name: "html"}, bodyFlag{name: "text"}
	// fields parses the shared create/update flags.
	fields := func() (h, t *string, f *api.EmailFormat, sd any, err error) {
		if h, err = html.value(); err != nil {
			return
		}
		if t, err = text.value(); err != nil {
			return
		}
		if f, err = enum("format", format, emailFormats...); err != nil {
			return
		}
		sd, err = jsonObject("sample-data", sample)
		return
	}
	create := annotate(&cobra.Command{
		Use: "create", Short: "Create an email template", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			h, t, f, sd, err := fields()
			if err != nil {
				return err
			}
			if h == nil {
				return apierr.Usage("one of --html or --html-file is required")
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateEmailTemplate(cmd.Context(), nil, api.CreateEmailTemplateV2Request{Name: name, Html: *h, Text: t, Format: f, Description: optional(desc), SampleData: sd})
			return app.finish(resp, err, show(func(t api.EmailTemplateDto) { fmt.Fprintln(app.Out, t.Id) }))
		},
	}, "V2CreateEmailTemplate", scopeAPIKeyOnly)
	update := annotate(&cobra.Command{
		Use: "update ID", Short: "Change an email template (only given flags change)", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, t, f, sd, err := fields()
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2UpdateEmailTemplate(cmd.Context(), args[0], nil, api.UpdateEmailTemplateV2Request{Name: optional(name), Html: h, Text: t, Format: f, Description: optional(desc), SampleData: sd})
			return app.finish(resp, err, app.done("Email template updated"))
		},
	}, "V2UpdateEmailTemplate", scopeAPIKeyOnly)
	for _, w := range []*cobra.Command{create, update} {
		w.Flags().StringVar(&name, "name", "", "template name (letters, digits, hyphens and underscores)")
		html.bind(w, "HTML body (Liquid; MJML with --format mjml)")
		text.bind(w, "plain-text body (Liquid)")
		w.Flags().StringVar(&format, "format", "", "html or mjml")
		w.Flags().StringVar(&desc, "description", "", "description")
		w.Flags().StringVar(&sample, "sample-data", "", "sample merge data as a JSON object")
	}
	_ = create.MarkFlagRequired("name")
	del := annotate(&cobra.Command{
		Use: "delete ID", Short: "Delete an email template", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteEmailTemplate(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Email template deleted"))
		},
	}, "V2DeleteEmailTemplate", scopeAPIKeyOnly)
	var data string
	render := annotate(&cobra.Command{
		Use: "render ID", Short: "Render an email template with merge data (default: its sample data)", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := jsonObject("data", data)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2RenderEmailTemplate(cmd.Context(), args[0], nil, api.RenderEmailTemplateV2Request{Data: d})
			return app.finish(resp, err, show(func(r api.RenderedEmailDto) { app.printBodies(r.Html, r.Text) }))
		},
	}, "V2RenderEmailTemplate", scopeAPIKeyOnly)
	render.Flags().StringVar(&data, "data", "", "merge data as a JSON object")
	c.AddCommand(list, get, create, update, del, render)
	return c
}

func printDomain(app *App, d api.SendingDomainDto) {
	app.printer().KV([][2]string{{"ID", d.Id}, {"Domain", d.Domain}, {"Status", string(d.Status)}, {"Needs upgrade", yesNo(d.NeedsUpgrade)},
		{"Created", fmtTime(&d.CreatedAt)}, {"Verified", fmtTime(d.VerifiedAt)}, {"Verify by", fmtTime(d.ExpiresAt)}})
	if d.Records == nil || len(*d.Records) == 0 {
		return
	}
	fmt.Fprintln(app.Out, "\nDNS records to publish:")
	rows := make([][]string, len(*d.Records))
	for i, r := range *d.Records {
		rows[i] = []string{r.Type, r.Name, r.Value, string(r.Purpose), yesNo(r.Required), string(r.Status)}
	}
	app.printer().Table([]string{"TYPE", "NAME", "VALUE", "PURPOSE", "REQUIRED", "STATUS"}, rows)
}

func newEmailDomains(app *App) *cobra.Command {
	c := &cobra.Command{Use: "domains", Short: "Domains you send email from"}
	var p paging
	list := annotate(&cobra.Command{
		Use: "list", Short: "List sending domains", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListEmailDomains(cmd.Context(), &api.V2ListEmailDomainsParams{Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"ID", "DOMAIN", "STATUS", "VERIFIED", "VERIFY BY", "NEEDS UPGRADE"}, func(d api.SendingDomainDto) []string {
				return []string{d.Id, d.Domain, string(d.Status), fmtTime(d.VerifiedAt), fmtTime(d.ExpiresAt), yesNo(d.NeedsUpgrade)}
			}))
		},
	}, "V2ListEmailDomains", "connect:email.read")
	p.bind(list)
	get := annotate(&cobra.Command{
		Use: "get ID", Short: "Show a sending domain and the DNS records to publish", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetEmailDomain(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(d api.SendingDomainDto) { printDomain(app, d) }))
		},
	}, "V2GetEmailDomain", "connect:email.read")
	add := annotate(&cobra.Command{
		Use: "add DOMAIN", Short: "Add a sending domain and show the DNS records to publish, then run `email domains verify`", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateEmailDomain(cmd.Context(), nil, api.CreateEmailDomainV2Request{Domain: args[0]})
			return app.finish(resp, err, show(func(d api.SendingDomainDto) { printDomain(app, d) }))
		},
	}, "V2CreateEmailDomain", "connect:email.setup")
	verify := annotate(&cobra.Command{
		Use: "verify ID", Short: "Check a domain's DNS records again; it becomes active once the required ones verify", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2VerifyEmailDomain(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(d api.SendingDomainDto) { printDomain(app, d) }))
		},
	}, "V2VerifyEmailDomain", "connect:email.setup")
	del := annotate(&cobra.Command{
		Use: "delete ID", Short: "Remove a sending domain", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteEmailDomain(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Domain removed"))
		},
	}, "V2DeleteEmailDomain", scopeAPIKeyOnly)
	c.AddCommand(list, get, add, verify, del)
	return c
}

func newEmailIncludes(app *App) *cobra.Command {
	c := &cobra.Command{Use: "includes", Short: "MJML fragments templates pull in with mj-include (API key only)"}
	list := annotate(&cobra.Command{
		Use: "list", Short: "List includes", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListEmailIncludes(cmd.Context(), nil)
			return app.finish(resp, err, listOf(app, []string{"NAME", "UPDATED"}, func(i api.EmailIncludeDto) []string { return []string{i.Name, fmtTime(i.UpdatedAt)} }))
		},
	}, "V2ListEmailIncludes", scopeAPIKeyOnly)
	get := annotate(&cobra.Command{
		Use: "get NAME", Short: "Print an include's MJML", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetEmailInclude(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(func(i api.EmailIncludeDto) { app.printer().Println(i.Content) }))
		},
	}, "V2GetEmailInclude", scopeAPIKeyOnly)
	var name string
	content := bodyFlag{name: "content"}
	create := annotate(&cobra.Command{
		Use: "create", Short: "Store an include, replacing any with the same name", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := content.value()
			if err != nil {
				return err
			}
			if body == nil {
				return apierr.Usage("one of --content or --content-file is required")
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateEmailInclude(cmd.Context(), nil, api.CreateEmailIncludeV2Request{Name: name, Content: *body})
			return app.finish(resp, err, app.done("Include saved"))
		},
	}, "V2CreateEmailInclude", scopeAPIKeyOnly)
	create.Flags().StringVar(&name, "name", "", "the name templates include it by, e.g. header.mjml")
	content.bind(create, "MJML content")
	_ = create.MarkFlagRequired("name")
	del := annotate(&cobra.Command{
		Use: "delete NAME", Short: "Delete an include", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteEmailInclude(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Include deleted"))
		},
	}, "V2DeleteEmailInclude", scopeAPIKeyOnly)
	c.AddCommand(list, get, create, del)
	return c
}

func newEmailTestRecipients(app *App) *cobra.Command {
	c := &cobra.Command{Use: "test-recipients", Short: "Addresses the shared test sender can reach"}
	list := annotate(&cobra.Command{
		Use: "list", Short: "List test recipients", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListEmailTestRecipients(cmd.Context(), nil)
			return app.finish(resp, err, listOf(app, []string{"ID", "EMAIL", "CONFIRMED", "ADDED"}, func(r api.TestRecipientDto) []string {
				return []string{r.Id, r.Email, yesNo(r.Verified), fmtTime(&r.CreatedAt)}
			}))
		},
	}, "V2ListEmailTestRecipients", "connect:email.read")
	add := annotate(&cobra.Command{
		Use: "add EMAIL", Short: "Add a test recipient (up to 5); usable once the link emailed to it is followed", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateEmailTestRecipient(cmd.Context(), nil, api.CreateEmailTestRecipientV2Request{Email: args[0]})
			return app.finish(resp, err, show(func(r api.TestRecipientDto) {
				fmt.Fprintln(app.Out, r.Id)
				fmt.Fprintf(app.Err, "A confirmation link has been emailed to %s; it can receive test emails once that is followed.\n", output.Clean(r.Email))
			}))
		},
	}, "V2CreateEmailTestRecipient", "connect:email.setup")
	del := annotate(&cobra.Command{
		Use: "delete ID", Short: "Remove a test recipient", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteEmailTestRecipient(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Test recipient removed"))
		},
	}, "V2DeleteEmailTestRecipient", scopeAPIKeyOnly)
	c.AddCommand(list, add, del)
	return c
}
