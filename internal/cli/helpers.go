package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/output"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"
)

// show decodes a success body into T and hands it to fn; used as finish's human renderer.
func show[T any](fn func(T)) func([]byte) error {
	return func(body []byte) error {
		var v T
		if len(body) > 0 {
			if err := json.Unmarshal(body, &v); err != nil {
				return err
			}
		}
		fn(v)
		return nil
	}
}

// listPage is the v2 list envelope.
type listPage[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"nextCursor"`
}

// listOf renders a v2 list as a table and says how to fetch the next page.
func listOf[T any](app *App, headers []string, row func(T) []string) func([]byte) error {
	return show(func(r listPage[T]) {
		rows := make([][]string, len(r.Data))
		for i, v := range r.Data {
			rows[i] = row(v)
		}
		app.printer().Table(headers, rows)
		if c := str(r.NextCursor); c != "" {
			fmt.Fprintf(app.Err, "More results: rerun with --cursor %s for the next page.\n", output.Clean(c))
		}
	})
}

// paging binds --limit and --cursor for cursor-paged lists.
type paging struct {
	limit  int32
	cursor string
}

func (p *paging) bind(c *cobra.Command) {
	c.Flags().Int32Var(&p.limit, "limit", 0, "page size, 1 to 200 (default 50)")
	c.Flags().StringVar(&p.cursor, "cursor", "", "nextCursor from the previous page")
}

// enum accepts one of the allowed snake_case values, case-insensitively; empty means not given.
func enum[T ~string](flag, v string, allowed ...T) (*T, error) {
	if v == "" {
		return nil, nil
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		if strings.EqualFold(v, string(a)) {
			return &a, nil
		}
		names[i] = string(a)
	}
	return nil, apierr.Usage(fmt.Sprintf("--%s must be one of %s", flag, strings.Join(names, ", ")))
}

// done prints a fixed confirmation line for 204 responses.
func (a *App) done(msg string) func([]byte) error {
	return func([]byte) error { fmt.Fprintln(a.Out, msg); return nil }
}

func parseDate(flag, s string) (*openapi_types.Date, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, apierr.Usage(fmt.Sprintf("--%s must be YYYY-MM-DD", flag))
	}
	return &openapi_types.Date{Time: t}, nil
}

func sliceOrNil(v []string) *[]string {
	if len(v) == 0 {
		return nil
	}
	return &v
}

func fmtTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// money renders a v2 amount as "£0.0350 / €0.0400"; empty when there is none.
func money(m *api.MoneyDto) string {
	if m == nil {
		return ""
	}
	var parts []string
	if m.Gbp != nil {
		parts = append(parts, "£"+*m.Gbp)
	}
	if m.Eur != nil {
		parts = append(parts, "€"+*m.Eur)
	}
	return strings.Join(parts, " / ")
}

// clip flattens whitespace and shortens text for a table cell.
func clip(s *string) string {
	t := []rune(strings.Join(strings.Fields(str(s)), " "))
	if len(t) > 50 {
		return string(t[:49]) + "…"
	}
	return string(t)
}
