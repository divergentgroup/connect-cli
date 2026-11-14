package cli

import (
	"fmt"
	"strings"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	legalForms = []api.BusinessLegalForm{api.BusinessLegalFormLimitedCompany, api.BusinessLegalFormPublicCompany, api.BusinessLegalFormSoleTrader,
		api.BusinessLegalFormPartnership, api.BusinessLegalFormNonProfit, api.BusinessLegalFormOther}
	useCases = []api.BusinessUseCase{api.BusinessUseCaseMarketing, api.BusinessUseCaseAlerts, api.BusinessUseCaseAuthentication,
		api.BusinessUseCaseConversations, api.BusinessUseCaseOther}
)

func newBusiness(app *App) *cobra.Command {
	c := &cobra.Command{Use: "business", Short: "The business profile that sender names are approved against"}
	c.AddCommand(newBusinessShow(app), newBusinessCompanies(app), newBusinessCompany(app), newBusinessSet(app), newBusinessSubmit(app))
	return c
}

// printProfile prints the main fields, what is still missing and the pointers a person may need.
func (a *App) printProfile(p api.BusinessProfileV2Dto) {
	form, use := "", ""
	if p.LegalForm != nil {
		form = string(*p.LegalForm)
	}
	if p.UseCase != nil {
		use = string(*p.UseCase)
	}
	a.printer().KV([][2]string{{"Status", string(p.Status)}, {"Legal name", str(p.LegalName)}, {"Legal form", form}, {"Registration number", str(p.RegistrationNumber)},
		{"Country", p.Country}, {"Address", str(p.AddressLine)}, {"City", str(p.City)}, {"Postcode", str(p.PostalCode)}, {"Website", str(p.Website)},
		{"Contact email", str(p.ContactEmail)}, {"Contact phone", str(p.ContactPhone)}, {"Use case", use}, {"Content links", str(p.ContentUrlsAndNumbers)},
		{"VAT number", str(p.VatNumber)}, {"Rejection reason", str(p.RejectionReason)}})
	for _, m := range p.Missing {
		fmt.Fprintf(a.Out, "Missing %s: %s\n", output.Clean(m.Field), output.Clean(m.Message))
	}
	if p.HeldRequests > 0 {
		fmt.Fprintf(a.Out, "Held sender requests: %d\n", p.HeldRequests)
	}
	if p.IdentityVerificationRequired {
		fmt.Fprintln(a.Out, "Identity verification is required: do it in the dashboard before submitting.")
	}
	if p.DashboardUrl != "" {
		fmt.Fprintf(a.Out, "Dashboard: %s\n", output.Clean(p.DashboardUrl))
	}
}

func newBusinessShow(app *App) *cobra.Command {
	return annotate(&cobra.Command{
		Use: "show", Short: "Show the business profile, its status and what is missing", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetBusinessProfile(cmd.Context(), nil)
			return app.finish(resp, err, show(app.printProfile))
		},
	}, "V2GetBusinessProfile", "connect:sms.senders.read")
}

func newBusinessCompanies(app *App) *cobra.Command {
	return annotate(&cobra.Command{
		Use: "companies QUERY", Short: "Search Companies House by company name", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2SearchCompanies(cmd.Context(), &api.V2SearchCompaniesParams{Q: args[0]})
			return app.finish(resp, err, listOf(app, []string{"NUMBER", "NAME", "STATUS", "ADDRESS"}, func(m api.CompanyMatchDto) []string {
				return []string{m.Number, m.Name, str(m.Status), str(m.Address)}
			}))
		},
	}, "V2SearchCompanies", "connect:sms.senders.read")
}

func newBusinessCompany(app *App) *cobra.Command {
	return annotate(&cobra.Command{
		Use: "company NUMBER", Short: "Use a Companies House company (from `business companies`) for the profile", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2ApplyCompany(cmd.Context(), nil, api.ApplyCompanyV2Request{Number: args[0]})
			return app.finish(resp, err, show(app.printProfile))
		},
	}, "V2ApplyCompany", "connect:sms.senders.write")
}

func newBusinessSet(app *App) *cobra.Command {
	var legalName, legalForm, country, address, city, postcode, website, email, phone, useCase, links string
	names := []string{"legal-name", "legal-form", "country", "address", "city", "postcode", "website", "contact-email", "contact-phone", "use-case", "content-links"}
	c := annotate(&cobra.Command{
		Use: "set", Short: "Update the business profile; only the flags you give are changed", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			given := false
			for _, n := range names {
				given = given || f.Changed(n)
			}
			if !given {
				return apierr.Usage("give at least one of --" + strings.Join(names, ", --"))
			}
			form, err := enum("legal-form", legalForm, legalForms...)
			if err != nil {
				return err
			}
			use, err := enum("use-case", useCase, useCases...)
			if err != nil {
				return err
			}
			changed := func(name string, v string) *string {
				if f.Changed(name) {
					return &v
				}
				return nil
			}
			req := api.UpdateBusinessProfileV2Request{LegalName: changed("legal-name", legalName), Country: changed("country", country), AddressLine: changed("address", address),
				City: changed("city", city), PostalCode: changed("postcode", postcode), Website: changed("website", website), ContactEmail: changed("contact-email", email),
				ContactPhone: changed("contact-phone", phone), ContentUrlsAndNumbers: changed("content-links", links), LegalForm: form, UseCase: use}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2UpdateBusinessProfile(cmd.Context(), nil, req)
			return app.finish(resp, err, show(func(p api.BusinessProfileV2Dto) {
				app.printProfile(p)
				if ig := ignoredFields(req, p); len(ig) > 0 {
					fmt.Fprintf(app.Err, "Warning: not changed (a submitted or verified profile only accepts contact details, and a company picked from Companies House is locked): %s\n", strings.Join(ig, ", "))
				}
			}))
		},
	}, "V2UpdateBusinessProfile", "connect:sms.senders.write")
	fl := c.Flags()
	fl.StringVar(&legalName, "legal-name", "", "registered or trading name")
	fl.StringVar(&legalForm, "legal-form", "", "limited_company, public_company, sole_trader, partnership, non_profit or other")
	fl.StringVar(&country, "country", "", "ISO 3166-1 alpha-2 country, for example GB")
	fl.StringVar(&address, "address", "", "first address line")
	fl.StringVar(&city, "city", "", "town or city")
	fl.StringVar(&postcode, "postcode", "", "postal code")
	fl.StringVar(&website, "website", "", "website or public page, such as example.com (empty clears it)")
	fl.StringVar(&email, "contact-email", "", "email address to contact about verification")
	fl.StringVar(&phone, "contact-phone", "", "contact phone in international format, for example +447700900123")
	fl.StringVar(&useCase, "use-case", "", "marketing, alerts, authentication, conversations or other")
	fl.StringVar(&links, "content-links", "", `links and phone numbers the messages will contain, or "none"`)
	return c
}

// ignoredFields names the non-contact fields that were sent but are not what the profile now holds.
func ignoredFields(r api.UpdateBusinessProfileV2Request, p api.BusinessProfileV2Dto) []string {
	same := func(sent *string, got string) bool {
		return sent == nil || strings.EqualFold(strings.TrimSpace(*sent), strings.TrimSpace(got))
	}
	site := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		for _, p := range []string{"https://", "http://", "www."} {
			s = strings.TrimPrefix(s, p)
		}
		return strings.TrimSuffix(s, "/")
	}
	var out []string
	add := func(ok bool, name string) {
		if !ok {
			out = append(out, name)
		}
	}
	add(same(r.LegalName, str(p.LegalName)), "legal-name")
	add(same(r.Country, p.Country), "country")
	add(same(r.AddressLine, str(p.AddressLine)), "address")
	add(same(r.City, str(p.City)), "city")
	add(same(r.PostalCode, str(p.PostalCode)), "postcode")
	add(r.Website == nil || site(*r.Website) == site(str(p.Website)), "website")
	add(r.LegalForm == nil || (p.LegalForm != nil && *r.LegalForm == *p.LegalForm), "legal-form")
	add(r.UseCase == nil || (p.UseCase != nil && *r.UseCase == *p.UseCase), "use-case")
	return out
}

func newBusinessSubmit(app *App) *cobra.Command {
	return annotate(&cobra.Command{
		Use: "submit", Short: "Submit the business profile for review", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2SubmitBusinessProfile(cmd.Context(), nil)
			return app.finish(resp, err, show(func(p api.BusinessProfileV2Dto) {
				fmt.Fprintf(app.Out, "Status: %s\n", p.Status)
				fmt.Fprintln(app.Out, "The Connect team will review the profile; held sender requests are released once it is verified.")
			}))
		},
	}, "V2SubmitBusinessProfile", "connect:sms.senders.write")
}
