package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/bulk"
	"github.com/spf13/cobra"
)

func contactRow(c api.ContactDto) []string {
	return []string{c.Id, c.Number, joinNonEmpty(str(c.FirstName), str(c.LastName)), str(c.Email), str(c.Company), str(c.ClientReference), strings.Join(c.Tags, ","), yesNo(c.Suppressed)}
}

var contactHeaders = []string{"ID", "NUMBER", "NAME", "EMAIL", "COMPANY", "CLIENT REF", "TAGS", "OPTED OUT"}

func (app *App) printContact(c api.ContactDto) {
	app.printer().KV([][2]string{{"ID", c.Id}, {"Number", c.Number}, {"Name", joinNonEmpty(str(c.FirstName), str(c.LastName))}, {"Email", str(c.Email)}, {"Company", str(c.Company)},
		{"Client ref", str(c.ClientReference)}, {"Tags", strings.Join(c.Tags, ",")}, {"Opted out", yesNo(c.Suppressed)}, {"Created", fmtTime(&c.CreatedAt)}})
}

type contactFlags struct {
	phone, first, last, email, company, ref string
	tags                                    []string
}

func (f *contactFlags) bind(c *cobra.Command, phoneRequired bool) {
	fl := c.Flags()
	fl.StringVar(&f.phone, "phone", "", "phone number (E.164)")
	fl.StringVar(&f.first, "first-name", "", "first name")
	fl.StringVar(&f.last, "last-name", "", "last name")
	fl.StringVar(&f.email, "email", "", "email")
	fl.StringVar(&f.company, "company", "", "company")
	fl.StringVar(&f.ref, "client-ref", "", "your reference for this contact")
	fl.StringArrayVar(&f.tags, "tag", nil, "tag, repeatable")
	if phoneRequired {
		_ = c.MarkFlagRequired("phone")
	}
}

func newContacts(app *App) *cobra.Command {
	c := &cobra.Command{Use: "contacts", Short: "Contacts and tags"}
	var search, ref, tag string
	var p paging
	list := annotate(&cobra.Command{
		Use: "list", Short: "List contacts, newest first", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListContacts(cmd.Context(), &api.V2ListContactsParams{Search: optional(search), ClientReference: optional(ref), Tag: optional(tag), Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, contactHeaders, contactRow))
		},
	}, "V2ListContacts", "connect:contacts.read")
	list.Flags().StringVar(&search, "search", "", "match name, number or email")
	list.Flags().StringVar(&ref, "client-ref", "", "filter by client reference")
	list.Flags().StringVar(&tag, "tag", "", "filter by tag")
	p.bind(list)

	get := annotate(&cobra.Command{
		Use: "get ID", Short: "Show a contact", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetContact(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(app.printContact))
		},
	}, "V2GetContact", "connect:contacts.read")

	var cf contactFlags
	create := annotate(&cobra.Command{
		Use: "create", Short: "Create a contact", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateContact(cmd.Context(), nil, api.CreateContactRequest{Number: cf.phone, FirstName: optional(cf.first), LastName: optional(cf.last), Email: optional(cf.email),
				Company: optional(cf.company), ClientReference: optional(cf.ref), Tags: sliceOrNil(cf.tags)})
			return app.finish(resp, err, show(func(r api.ContactDto) { fmt.Fprintln(app.Out, r.Id) }))
		},
	}, "V2CreateContact", "connect:contacts.write")
	cf.bind(create, true)

	var uf contactFlags
	update := annotate(&cobra.Command{
		Use: "update ID", Short: "Update fields on a contact (only given flags change)", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			var tags *[]string
			if cmd.Flags().Changed("tag") {
				tags = &uf.tags
			}
			resp, err := c.V2UpdateContact(cmd.Context(), args[0], nil, api.UpdateContactRequest{Number: optional(uf.phone), FirstName: optional(uf.first), LastName: optional(uf.last),
				Email: optional(uf.email), Company: optional(uf.company), ClientReference: optional(uf.ref), Tags: tags})
			return app.finish(resp, err, show(app.printContact))
		},
	}, "V2UpdateContact", "connect:contacts.write")
	uf.bind(update, false)

	del := annotate(&cobra.Command{
		Use: "delete ID", Short: "Delete a contact", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteContact(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Contact deleted"))
		},
	}, "V2DeleteContact", scopeAPIKeyOnly)

	tagsCmd := annotate(&cobra.Command{
		Use: "tags", Short: "List all tags in use", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListContactTags(cmd.Context(), nil)
			return app.finish(resp, err, show(func(r listPage[string]) {
				for _, t := range r.Data {
					app.printer().Println(t)
				}
			}))
		},
	}, "V2ListContactTags", "connect:contacts.read")

	var add, remove []string
	tagCmd := annotate(&cobra.Command{
		Use: "tag ID...", Short: "Add or remove tags on contacts", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(add)+len(remove) == 0 {
				return apierr.Usage("give at least one --add or --remove")
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2TagContacts(cmd.Context(), nil, api.TagContactsRequest{ContactIds: args, Tags: sliceOrNil(add), RemoveTags: sliceOrNil(remove)})
			return app.finish(resp, err, show(func(r api.TagContactsResponse) { fmt.Fprintf(app.Out, "%d contact(s) updated\n", r.Updated) }))
		},
	}, "V2TagContacts", "connect:contacts.write")
	tagCmd.Flags().StringArrayVar(&add, "add", nil, "tag to add, repeatable")
	tagCmd.Flags().StringArrayVar(&remove, "remove", nil, "tag to remove, repeatable")

	var file string
	var maps []string
	var strict bool
	upsert := annotate(&cobra.Command{
		Use:   "upsert",
		Short: "Create or update contacts from a CSV (columns: phone, firstName, lastName, email, company, clientReference, tags)",
		Long:  "Chunks of 500 are sent with a deterministic Idempotency-Key and recorded in a manifest, so a rerun resumes rather than repeating.",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			overrides, err := parseMaps(maps)
			if err != nil {
				return err
			}
			return app.bulkRun(cmd, file, overrides, strict, func(c *api.Client, key string, rows []map[string]string) (*http.Response, error) {
				cs := make([]api.CreateContactRequest, len(rows))
				for i, r := range rows {
					var tags *[]string
					if r["tags"] != "" {
						tags = ptr(strings.Split(r["tags"], ";"))
					}
					cs[i] = api.CreateContactRequest{Number: firstOf(r, "phone", "number", "phonenumber", "mobile", "recipient"), FirstName: optional(r["firstname"]), LastName: optional(r["lastname"]),
						Email: optional(r["email"]), Company: optional(r["company"]), ClientReference: optional(r["clientreference"]), Tags: tags}
				}
				return c.V2BatchUpsertContacts(cmd.Context(), &api.V2BatchUpsertContactsParams{IdempotencyKey: &key}, api.BatchUpsertContactsRequest{Contacts: cs})
			}, func(body []byte) bulk.ChunkResult {
				var r api.ContactBatchResultDto
				_ = json.Unmarshal(body, &r)
				res := bulk.ChunkResult{Accepted: int(r.Created + r.Updated)}
				for _, e := range r.Errors {
					res.Errors = append(res.Errors, bulk.RowError{Row: int(e.Index), Error: e.Error})
				}
				return res
			})
		},
	}, "V2BatchUpsertContacts", "connect:contacts.write")
	upsert.Flags().StringVar(&file, "file", "", "CSV file")
	upsert.Flags().StringArrayVar(&maps, "map", nil, "column=field mapping override, repeatable")
	upsert.Flags().BoolVar(&strict, "strict", false, "exit 4 if any row is rejected")
	_ = upsert.MarkFlagRequired("file")

	c.AddCommand(list, get, create, update, del, tagsCmd, tagCmd, upsert)
	return c
}

func firstOf(row map[string]string, keys ...string) string {
	for _, k := range keys {
		if row[k] != "" {
			return row[k]
		}
	}
	return ""
}

// parseRules accepts field:op:value triples in v2 snake_case, e.g. tag:equals:vip.
func parseRules(rules []string) (*[]api.ContactListRuleDto, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	out := make([]api.ContactListRuleDto, len(rules))
	for i, r := range rules {
		parts := strings.SplitN(r, ":", 3)
		if len(parts) != 3 {
			return nil, apierr.Usage("--rule expects field:op:value, e.g. tag:equals:vip")
		}
		out[i] = api.ContactListRuleDto{Field: api.ContactListRuleField(strings.ToLower(parts[0])), Op: api.ContactListRuleOp(strings.ToLower(parts[1])), Value: parts[2]}
	}
	return &out, nil
}

func newLists(app *App) *cobra.Command {
	c := &cobra.Command{Use: "lists", Short: "Contact lists and segments"}
	var p paging
	list := annotate(&cobra.Command{
		Use: "list", Short: "List contact lists, newest first", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListContactLists(cmd.Context(), &api.V2ListContactListsParams{Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"ID", "NAME", "KIND", "DESCRIPTION"}, func(l api.ContactListDto) []string {
				return []string{l.Id, l.Name, string(l.Kind), str(l.Description)}
			}))
		},
	}, "V2ListContactLists", "connect:contacts.read")
	p.bind(list)
	printList := func(l api.ContactListDto) {
		var rules []string
		if l.Rules != nil {
			for _, r := range *l.Rules {
				rules = append(rules, fmt.Sprintf("%s:%s:%s", r.Field, r.Op, r.Value))
			}
		}
		app.printer().KV([][2]string{{"ID", l.Id}, {"Name", l.Name}, {"Kind", string(l.Kind)}, {"Description", str(l.Description)}, {"Rules", strings.Join(rules, " ")}, {"Created", fmtTime(&l.CreatedAt)}})
	}
	get := annotate(&cobra.Command{
		Use: "get ID", Short: "Show a list", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetContactList(cmd.Context(), args[0], nil)
			return app.finish(resp, err, show(printList))
		},
	}, "V2GetContactList", "connect:contacts.read")
	var sample int32
	preview := annotate(&cobra.Command{
		Use: "preview ID", Short: "Count members and show a sample", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2PreviewContactList(cmd.Context(), args[0], &api.V2PreviewContactListParams{SampleSize: optional(sample)})
			return app.finish(resp, err, show(func(r api.ContactListPreviewDto) {
				fmt.Fprintf(app.Out, "%d contact(s)\n", r.Total)
				rows := make([][]string, len(r.Sample))
				for i, v := range r.Sample {
					rows[i] = contactRow(v)
				}
				app.printer().Table(contactHeaders, rows)
			}))
		},
	}, "V2PreviewContactList", "connect:contacts.read")
	preview.Flags().Int32Var(&sample, "sample", 0, "sample size")

	var name, desc, typ string
	var rules []string
	create := annotate(&cobra.Command{
		Use: "create", Short: "Create a list", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			k, err := enum("type", typ, api.Static, api.Dynamic)
			if err != nil {
				return err
			}
			rs, err := parseRules(rules)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateContactList(cmd.Context(), nil, api.CreateContactListRequest{Name: name, Description: optional(desc), Kind: k, Rules: rs})
			return app.finish(resp, err, show(func(l api.ContactListDto) { fmt.Fprintln(app.Out, l.Id) }))
		},
	}, "V2CreateContactList", "connect:contacts.write")
	create.Flags().StringVar(&typ, "type", "", "static or dynamic")
	update := annotate(&cobra.Command{
		Use: "update ID", Short: "Rename a list or change its rules", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rs, err := parseRules(rules)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2UpdateContactList(cmd.Context(), args[0], nil, api.UpdateContactListRequest{Name: optional(name), Description: optional(desc), Rules: rs})
			return app.finish(resp, err, show(printList))
		},
	}, "V2UpdateContactList", "connect:contacts.write")
	for _, w := range []*cobra.Command{create, update} {
		w.Flags().StringVar(&name, "name", "", "list name")
		w.Flags().StringVar(&desc, "description", "", "description")
		w.Flags().StringArrayVar(&rules, "rule", nil, "rule field:op:value, repeatable (dynamic lists), e.g. tag:equals:vip")
	}
	_ = create.MarkFlagRequired("name")
	del := annotate(&cobra.Command{
		Use: "delete ID", Short: "Delete a list (its contacts are kept)", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteContactList(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("List deleted"))
		},
	}, "V2DeleteContactList", scopeAPIKeyOnly)
	addM := annotate(&cobra.Command{
		Use: "add-members ID CONTACT_ID...", Short: "Add contacts to a static list", Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			ids := args[1:]
			resp, err := c.V2AddContactListContacts(cmd.Context(), args[0], nil, api.AddContactListContactsRequest{ContactIds: &ids})
			return app.finish(resp, err, show(func(r api.MembershipChangeDto) { fmt.Fprintf(app.Out, "Added %d, skipped %d\n", r.Added, r.Skipped) }))
		},
	}, "V2AddContactListContacts", "connect:contacts.write")
	rmM := annotate(&cobra.Command{
		Use: "remove-members ID CONTACT_ID...", Short: "Remove contacts from a static list", Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2RemoveContactListContacts(cmd.Context(), args[0], nil, api.RemoveContactListContactsRequest{ContactIds: args[1:]})
			return app.finish(resp, err, app.done("Members removed"))
		},
	}, "V2RemoveContactListContacts", "connect:contacts.write")
	c.AddCommand(list, get, preview, create, update, del, addM, rmM)
	return c
}

func newSuppressions(app *App) *cobra.Command {
	c := &cobra.Command{Use: "suppressions", Short: "Opt-out list (numbers and addresses that will not be messaged)"}
	var number, reason string
	var p paging
	list := annotate(&cobra.Command{
		Use: "list", Short: "List suppressions, newest first", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ListSuppressions(cmd.Context(), &api.V2ListSuppressionsParams{Number: optional(number), Limit: optional(p.limit), Cursor: optional(p.cursor)})
			return app.finish(resp, err, listOf(app, []string{"NUMBER OR EMAIL", "SOURCE", "REASON", "SINCE"}, func(s api.SuppressionDto) []string {
				return []string{joinNonEmpty(str(s.Number), str(s.Email)), string(s.Source), str(s.Reason), fmtTime(&s.CreatedAt)}
			}))
		},
	}, "V2ListSuppressions", "connect:contacts.read")
	list.Flags().StringVar(&number, "number", "", "only this number (E.164)")
	p.bind(list)
	add := annotate(&cobra.Command{
		Use: "add NUMBER", Short: "Opt a number out", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2CreateSuppression(cmd.Context(), nil, api.CreateSuppressionV2Request{Number: &args[0], Reason: optional(reason)})
			return app.finish(resp, err, app.done("Number suppressed"))
		},
	}, "V2CreateSuppression", "connect:contacts.write")
	add.Flags().StringVar(&reason, "reason", "", "why the number is suppressed")
	remove := annotate(&cobra.Command{
		Use: "remove NUMBER", Short: "Remove a number from the opt-out list (API key only)", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2DeleteSuppression(cmd.Context(), args[0], nil)
			return app.finish(resp, err, app.done("Suppression removed"))
		},
	}, "V2DeleteSuppression", scopeAPIKeyOnly)
	c.AddCommand(list, add, remove)
	return c
}
