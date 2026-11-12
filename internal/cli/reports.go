package cli

import (
	"fmt"

	"github.com/divergentgroup/connect-cli/internal/api"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"
)

type dateRange struct{ from, to string }

func (d *dateRange) bind(c *cobra.Command) {
	c.Flags().StringVar(&d.from, "from", "", "start date YYYY-MM-DD (UTC)")
	c.Flags().StringVar(&d.to, "to", "", "end date YYYY-MM-DD (UTC), no later than today")
}

func (d *dateRange) parse() (*openapi_types.Date, *openapi_types.Date, error) {
	from, err := parseDate("from", d.from)
	if err != nil {
		return nil, nil, err
	}
	to, err := parseDate("to", d.to)
	return from, to, err
}

func day(d openapi_types.Date) string { return d.Format("2006-01-02") }

func newReports(app *App) *cobra.Command {
	c := &cobra.Command{Use: "reports", Short: "Outbound SMS outcomes, latency, countries and client-tag spend; email deliverability"}
	var dr dateRange
	var agg string
	analytics := annotate(&cobra.Command{
		Use: "analytics", Short: "Sent, delivered, failed and in-progress counts per period", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, to, err := dr.parse()
			if err != nil {
				return err
			}
			a, err := enum("aggregation", agg, api.Daily, api.Weekly, api.Fortnightly)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetSendReport(cmd.Context(), &api.V2GetSendReportParams{Since: from, Until: to, Aggregation: a})
			return app.finish(resp, err, show(func(r api.ReportDtoOfSendReportDto) {
				rows := make([][]string, len(r.Data))
				for i, v := range r.Data {
					rows[i] = []string{day(v.Period.From), day(v.Period.To), fmt.Sprint(v.Sent), fmt.Sprint(v.Delivered), fmt.Sprint(v.Failed), fmt.Sprint(v.InProgress), fmt.Sprint(v.NoDeliveryReceipt)}
				}
				app.printer().Table([]string{"FROM", "TO", "SENT", "DELIVERED", "FAILED", "IN PROGRESS", "NO RECEIPT"}, rows)
			}))
		},
	}, "V2GetSendReport", "connect:reports.read")
	analytics.Flags().StringVar(&agg, "aggregation", "", "daily, weekly or fortnightly")
	latency := annotate(&cobra.Command{
		Use: "latency", Short: "Delivery latency buckets", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, to, err := dr.parse()
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetLatencyReport(cmd.Context(), &api.V2GetLatencyReportParams{Since: from, Until: to})
			return app.finish(resp, err, show(func(r api.GetLatencyReportResponse) {
				rows := make([][]string, len(r.Latency.Buckets))
				for i, b := range r.Latency.Buckets {
					rows[i] = []string{b.Label, fmt.Sprint(b.Count)}
				}
				app.printer().Table([]string{"BUCKET", "COUNT"}, rows)
				fmt.Fprintf(app.Err, "%d of %d message(s) measured\n", r.Latency.Measured, r.Latency.Total)
			}))
		},
	}, "V2GetLatencyReport", "connect:reports.read")
	countries := annotate(&cobra.Command{
		Use: "countries", Short: "Message volume by destination country", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, to, err := dr.parse()
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetCountryReport(cmd.Context(), &api.V2GetCountryReportParams{Since: from, Until: to})
			return app.finish(resp, err, show(func(r api.ReportDtoOfCountryReportDto) {
				rows := make([][]string, len(r.Data))
				for i, v := range r.Data {
					rows[i] = []string{str(v.Country), fmt.Sprint(v.Count)}
				}
				app.printer().Table([]string{"COUNTRY", "COUNT"}, rows)
			}))
		},
	}, "V2GetCountryReport", "connect:reports.read")
	var tag string
	tags := annotate(&cobra.Command{
		Use: "client-tags", Short: "Volume and cost per client tag", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, to, err := dr.parse()
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetClientTagReport(cmd.Context(), &api.V2GetClientTagReportParams{Since: from, Until: to, Tag: optional(tag)})
			return app.finish(resp, err, show(func(r api.ReportDtoOfClientTagReportDto) {
				rows := make([][]string, len(r.Data))
				for i, s := range r.Data {
					rows[i] = []string{s.Tag, fmt.Sprint(s.Sent), fmt.Sprint(s.Parts), money(&s.Cost)}
				}
				app.printer().Table([]string{"TAG", "SENT", "PARTS", "COST"}, rows)
			}))
		},
	}, "V2GetClientTagReport", "connect:reports.read")
	tags.Flags().StringVar(&tag, "tag", "", "only this client tag")
	email := annotate(&cobra.Command{
		Use: "email-deliverability", Short: "Email sent, delivered, bounced, complaint and open counts and rates per kind (default: last 7 days)", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, to, err := dr.parse()
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetEmailDeliverabilityReport(cmd.Context(), &api.V2GetEmailDeliverabilityReportParams{Since: from, Until: to})
			return app.finish(resp, err, show(func(r api.EmailDeliverabilityReportDto) {
				row := func(kind string, s api.EmailDeliverabilityStatsDto) []string {
					return []string{kind, fmt.Sprint(s.Sent), fmt.Sprint(s.Delivered), fmt.Sprint(s.HardBounced), fmt.Sprint(s.SoftBounced), fmt.Sprint(s.Complaints),
						fmt.Sprint(s.Opened), fmt.Sprint(s.Unsubscribed), fmt.Sprint(s.Suppressed), percent(s.DeliveryRate), percent(s.BounceRate), percent(s.ComplaintRate)}
				}
				var rows [][]string
				for _, k := range r.ByKind {
					rows = append(rows, row(string(k.Kind), k.Stats))
				}
				app.printer().Table([]string{"KIND", "SENT", "DELIVERED", "HARD BOUNCED", "SOFT BOUNCED", "COMPLAINTS", "OPENED", "UNSUBSCRIBED", "SUPPRESSED", "DELIVERY RATE", "BOUNCE RATE", "COMPLAINT RATE"},
					append(rows, row("total", r.Total)))
				fmt.Fprintf(app.Err, "From %s to %s\n", day(r.Period.From), day(r.Period.To))
			}))
		},
	}, "V2GetEmailDeliverabilityReport", "connect:reports.read")
	for _, r := range []*cobra.Command{analytics, latency, countries, tags, email} {
		dr.bind(r)
	}
	c.AddCommand(analytics, latency, countries, tags, email)
	return c
}

// percent renders a 0 to 1 rate as a percentage; empty when there is none.
func percent(r *float64) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("%.1f%%", *r*100)
}

func newBilling(app *App) *cobra.Command {
	c := &cobra.Command{Use: "billing", Short: "Spend and balance"}
	var granularity string
	var months int32
	spend := annotate(&cobra.Command{
		Use: "spend", Short: "SMS spend per day or month at current plan prices", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			g, err := enum("granularity", granularity, api.SpendGranularityDay, api.SpendGranularityMonth)
			if err != nil {
				return err
			}
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetWorkspaceSpend(cmd.Context(), &api.V2GetWorkspaceSpendParams{Granularity: g, Months: optional(months)})
			return app.finish(resp, err, show(func(r api.WorkspaceSpendDto) {
				rows := make([][]string, len(r.Spend))
				for i, b := range r.Spend {
					rows[i] = []string{day(b.Date), fmt.Sprint(b.Parts), fmt.Sprint(b.UnpricedParts), money(&b.Amount)}
				}
				app.printer().Table([]string{"DATE", "PARTS", "UNPRICED PARTS", "AMOUNT"}, rows)
				fmt.Fprintf(app.Err, "Total %s from %s to %s\n", money(&r.Total), day(r.Period.From), day(r.Period.To))
			}))
		},
	}, "V2GetWorkspaceSpend", "connect:reports.read")
	spend.Flags().StringVar(&granularity, "granularity", "", "day or month")
	spend.Flags().Int32Var(&months, "months", 0, "how many months back")
	balance := annotate(&cobra.Command{
		Use: "balance", Short: "Prepaid balance", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := app.client(cmd)
			if err != nil {
				return err
			}
			resp, err := c.V2GetWorkspaceSpend(cmd.Context(), &api.V2GetWorkspaceSpendParams{Granularity: ptr(api.SpendGranularityMonth), Months: ptr(int32(1))})
			return app.finish(resp, err, show(func(r api.WorkspaceSpendDto) {
				if r.Balance == nil {
					fmt.Fprintln(app.Out, "No prepaid balance for this workspace")
					return
				}
				fmt.Fprintln(app.Out, money(r.Balance))
			}))
		},
	}, "V2GetWorkspaceSpend", "connect:reports.read")
	c.AddCommand(spend, balance)
	return c
}
