package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/bulk"
	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/divergentgroup/connect-cli/internal/output"
	"github.com/spf13/cobra"
)

// bulkRun loads a CSV, derives the import id and runs chunks through the manifest.
func (app *App) bulkRun(cmd *cobra.Command, file string, overrides map[string]string, strict bool,
	send func(c *api.Client, key string, rows []map[string]string) (*http.Response, error),
	parse func(body []byte) bulk.ChunkResult) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return apierr.Usage(err.Error())
	}
	rows, err := bulk.ReadCSV(bytes.NewReader(raw), overrides)
	if err != nil {
		return apierr.Usage(err.Error())
	}
	if len(rows) == 0 {
		return apierr.Usage("the file has no data rows")
	}
	c, cred, err := app.client(cmd)
	if err != nil {
		return err
	}
	app.sendNotice(cred)
	hash, _ := bulk.FileHash(bytes.NewReader(raw))
	stateDir, err := config.StateDir()
	if err != nil {
		return err
	}
	m, err := bulk.LoadManifest(stateDir, bulk.ImportID(hash, workspaceKey(cred)))
	if err != nil {
		return err
	}
	if len(m.Chunks) > 0 {
		fmt.Fprintf(app.Err, "Resuming import %s: %d chunk(s) already sent\n", m.ImportID, len(m.Chunks))
	}
	runErr := m.Run(len(rows), func(_ int, key string, start, end int) (bulk.ChunkResult, error) {
		resp, err := send(c, key, rows[start:end])
		if err != nil {
			return bulk.ChunkResult{}, fmt.Errorf("%w: %v", bulk.ErrTransport, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return bulk.ChunkResult{}, fmt.Errorf("%w: %v", bulk.ErrTransport, err)
		}
		if e := apierr.FromResponse(resp, body); e != nil {
			return bulk.ChunkResult{}, e
		}
		return parse(body), nil
	})
	results := m.Results()
	var rowErrors []bulk.RowError
	accepted := 0
	for _, r := range results {
		accepted += r.Accepted
		rowErrors = append(rowErrors, r.Errors...)
	}
	if app.JSON {
		_ = json.NewEncoder(app.Out).Encode(map[string]any{"importId": m.ImportID, "chunks": results, "accepted": accepted, "rowErrors": rowErrors})
	} else {
		for _, e := range rowErrors {
			fmt.Fprintf(app.Err, "row %d: %s\n", e.Row, output.Clean(e.Error))
		}
		fmt.Fprintf(app.Out, "Accepted %d of %d rows in %d chunk(s); import %s\n", accepted, len(rows), len(results), m.ImportID)
	}
	if runErr != nil {
		return runErr
	}
	if strict && len(rowErrors) > 0 {
		return &apierr.Error{Code: "validation", Message: fmt.Sprintf("%d row(s) were rejected (--strict)", len(rowErrors))}
	}
	return nil
}

func workspaceKey(cred *creds.Credential) string {
	if cred.WorkspaceOverride != "" {
		return cred.WorkspaceOverride
	}
	if cred.Profile.WorkspaceID != "" {
		return cred.Profile.WorkspaceID
	}
	return cred.Profile.Name
}

func newSmsSendBulk(app *App) *cobra.Command {
	var file, from, kind, at, tag string
	var strict bool
	var maps []string
	c := annotate(&cobra.Command{
		Use:   "send-bulk",
		Short: "Send many SMS from a CSV (columns: recipient, sender, content, clientReference, kind); resumable and idempotent",
		Long: `Reads the CSV client-side and sends it as batches of 500 with a deterministic Idempotency-Key per batch,
so a lost response is retried safely and rerunning the command resumes from the saved manifest.
Rows the API rejects are printed with their row number; exit 0 unless --strict is given.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			k, err := enum("kind", kind, smsKinds...)
			if err != nil {
				return err
			}
			sendAt, err := parseTime("at", at)
			if err != nil {
				return err
			}
			overrides, err := parseMaps(maps)
			if err != nil {
				return err
			}
			return app.bulkRun(cmd, file, overrides, strict, func(c *api.Client, key string, rows []map[string]string) (*http.Response, error) {
				msgs := make([]api.SmsBatchMessageDto, len(rows))
				for i, r := range rows {
					rk, err := enum("kind", r["kind"], smsKinds...)
					if err != nil {
						return nil, err
					}
					msgs[i] = api.SmsBatchMessageDto{Recipient: optional(r["recipient"]), Sender: optional(r["sender"]), Content: optional(r["content"]), ClientReference: optional(r["clientreference"]), Kind: rk}
				}
				return c.V2CreateSmsBatch(cmd.Context(), &api.V2CreateSmsBatchParams{IdempotencyKey: &key}, api.CreateSmsBatchV2Request{Messages: &msgs,
					Defaults: &api.SmsBatchDefaultsDto{Sender: optional(from), Kind: k, ScheduledAt: sendAt, ClientTag: optional(tag)}})
			}, func(body []byte) bulk.ChunkResult {
				var b api.SmsBatchDto
				_ = json.Unmarshal(body, &b)
				res := bulk.ChunkResult{BatchID: b.Id, Accepted: int(b.Counts.Total)}
				for _, e := range b.Errors {
					res.Errors = append(res.Errors, bulk.RowError{Row: int(e.Index), Error: e.Detail})
				}
				return res
			})
		},
	}, "V2CreateSmsBatch", "connect:sms.send.live")
	f := c.Flags()
	f.StringVar(&file, "file", "", "CSV file")
	f.StringVar(&from, "from", "", "sender for rows without one")
	f.StringVar(&kind, "kind", "marketing", "message kind for rows without one")
	f.StringVar(&at, "at", "", "schedule for an RFC 3339 time (UTC)")
	f.StringVar(&tag, "client-tag", "", "tag for spend reporting")
	f.StringArrayVar(&maps, "map", nil, "column=field mapping override, repeatable")
	f.BoolVar(&strict, "strict", false, "exit 4 if any row is rejected")
	_ = c.MarkFlagRequired("file")
	return c
}

func parseMaps(maps []string) (map[string]string, error) {
	out := map[string]string{}
	for _, m := range maps {
		col, field, ok := strings.Cut(m, "=")
		if !ok {
			return nil, apierr.Usage("--map expects column=field")
		}
		out[col] = field
	}
	return out, nil
}

func newSmsSendList(app *App) *cobra.Command {
	var lists, exclude []string
	var from, content, tmpl, data, kind, at, tag, key string
	var skipHours int32
	var dryRun bool
	c := annotate(&cobra.Command{
		Use:   "send-list",
		Short: "Send to one or more contact lists (needs sms.send.live); --dry-run previews count and cost",
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
			td, err := parseTemplateData(data)
			if err != nil {
				return err
			}
			c, cred, err := app.client(cmd)
			if err != nil {
				return err
			}
			body := api.CreateSmsBatchV2Request{List: &api.SmsBatchListDto{Ids: &lists, ExcludeIds: sliceOrNil(exclude)}, Sender: optional(from), Content: optional(content),
				Template: template(tmpl, td), Kind: k, ScheduledAt: sendAt, ClientTag: optional(tag), SkipMessagedWithinHours: optional(skipHours)}
			if dryRun {
				resp, err := c.V2PreviewSmsBatch(cmd.Context(), nil, body)
				return app.finish(resp, err, show(func(r api.SmsBatchPreviewDto) {
					app.printer().KV([][2]string{{"Recipients", fmt.Sprint(r.Recipients)}, {"Parts", fmt.Sprint(r.Parts)}, {"Estimated cost", money(r.Cost)}, {"Payment required", yesNo(r.PaymentRequired)},
						{"Skipped", fmt.Sprintf("suppressed %d, recently messaged %d, invalid %d", r.Skipped.Suppressed, r.Skipped.RecentlyMessaged, r.Skipped.Invalid)}, {"Estimated completion", fmtTime(r.EstimatedCompletionAt)}})
					for _, e := range r.Errors {
						fmt.Fprintf(app.Err, "row %d: %s\n", e.Index+1, output.Clean(e.Detail))
					}
				}))
			}
			app.sendNotice(cred)
			resp, err := c.V2CreateSmsBatch(cmd.Context(), &api.V2CreateSmsBatchParams{IdempotencyKey: optional(key)}, body)
			return app.finish(resp, err, show(func(b api.SmsBatchDto) { printBatch(app, b) }))
		},
	}, "V2CreateSmsBatch,V2PreviewSmsBatch", "connect:sms.send.live")
	f := c.Flags()
	f.StringArrayVar(&lists, "list", nil, "list id, repeatable")
	f.StringArrayVar(&exclude, "exclude-list", nil, "list id to exclude, repeatable")
	f.StringVar(&from, "from", "", "sender name or number")
	f.StringVar(&content, "content", "", "message text")
	f.StringVar(&tmpl, "template", "", "stored template name instead of --content")
	f.StringVar(&data, "data", "", "template merge data as a JSON object")
	f.StringVar(&kind, "kind", "marketing", "transactional, marketing or authentication")
	f.StringVar(&at, "at", "", "schedule for an RFC 3339 time (UTC)")
	f.StringVar(&tag, "client-tag", "", "tag for spend reporting")
	f.StringVar(&key, "idempotency-key", "", "key so a retry does not double-send")
	f.Int32Var(&skipHours, "skip-messaged-within-hours", 0, "skip contacts messaged in the last N hours")
	f.BoolVar(&dryRun, "dry-run", false, "preview the count and cost without sending")
	_ = c.MarkFlagRequired("list")
	return c
}
