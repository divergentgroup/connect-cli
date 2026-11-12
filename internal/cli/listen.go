package cli

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/divergentgroup/connect-cli/internal/api"
	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/auth"
	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/output"
	"github.com/spf13/cobra"
)

const listenPage = 200

// webhookComponents are what Connect covers when it signs a v2 webhook delivery (RFC 9421).
var webhookComponents = []string{"@method", "@authority", "@path", "@query", "content-digest", "content-type", "webhook-id"}

func newListen(app *App) *cobra.Command {
	var forward, types, secret string
	var interval time.Duration
	c := annotate(&cobra.Command{
		Use:   "listen",
		Short: "Forward new events to a local URL, signed like webhook deliveries",
		Long: `Polls the workspace event feed and POSTs each event that happens after it starts to --forward-to,
with the same body and RFC 9421 signature headers a v2 webhook delivery carries. The signing secret is
printed at start; pass --secret to keep the same one across runs. Ctrl-C stops it.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if forward == "" {
				return apierr.Usage("--forward-to is required")
			}
			target, err := url.Parse(forward)
			if err != nil || config.CheckOrigin(forward) != nil || target.Host == "" {
				return apierr.Usage("--forward-to must be an https URL, or http on localhost")
			}
			if interval < time.Second {
				return apierr.Usage("--interval must be at least 1s")
			}
			if secret == "" {
				b := make([]byte, 32)
				_, _ = rand.Read(b)
				secret = base64.StdEncoding.EncodeToString(b)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			l, err := newListener(ctx, app, target, secret, types)
			if err != nil {
				return err
			}
			fmt.Fprintf(app.Err, "Forwarding events to %s (Ctrl-C to stop)\nSigning secret: %s (keyid 1)\n", output.Clean(forward), secret)
			wait := interval
			for {
				full, err := l.poll(ctx)
				if ctx.Err() != nil {
					return nil
				}
				var ae *apierr.Error
				switch {
				case err == nil:
					wait = interval
					if full {
						wait = 0
					}
				case errors.As(err, &ae) && ae.Code == "cursor_expired":
					l.cursor, l.since = "", time.Now().UTC()
				case errors.As(err, &ae) && ae.ExitCode() == 5:
					wait = min(wait*2, 30*time.Second)
					fmt.Fprintf(app.Err, "Polling failed, retrying in %s: %s\n", wait, output.Clean(ae.Message))
				default:
					return err
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(wait):
				}
			}
		},
	}, "V2ListEvents", "connect:sms.read")
	c.Flags().StringVar(&forward, "forward-to", "", "URL to POST each event to, e.g. http://localhost:3000/webhooks")
	c.Flags().StringVar(&types, "events", "", "only these event types, comma separated, e.g. sms.delivered,sms.received")
	c.Flags().StringVar(&secret, "secret", "", "signing secret to use instead of a new random one")
	c.Flags().DurationVar(&interval, "interval", 2*time.Second, "how often to poll the event feed (at least 1s)")
	return c
}

type listener struct {
	app       *App
	api       *api.Client
	target    *url.URL
	secret    string
	types     string
	workspace string
	cursor    string
	since     time.Time
	out       *http.Client
}

func newListener(ctx context.Context, app *App, target *url.URL, secret, types string) (*listener, error) {
	cred, err := app.credential(ctx)
	if err != nil {
		return nil, err
	}
	store, err := app.store()
	if err != nil {
		return nil, err
	}
	u, err := app.apiURL()
	if err != nil {
		return nil, err
	}
	// A long run outlives an access token: refresh before each request when it is close to expiry.
	fresh := func(ctx context.Context, req *http.Request) error {
		p, err := auth.Refresh(ctx, store, cred.Profile, app.issuer())
		if err != nil {
			return err
		}
		cred.Profile = p
		return cred.ApplyV2(ctx, req)
	}
	c, err := api.NewClient(u, api.WithRequestEditorFn(fresh), api.WithHTTPClient(app.httpClient()))
	if err != nil {
		return nil, err
	}
	l := &listener{app: app, api: c, target: target, secret: secret, types: types, since: time.Now().UTC(),
		out: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	resp, err := c.V2GetWorkspace(ctx, nil)
	body, err := readOK(resp, err)
	if err != nil {
		return nil, err
	}
	var w api.WorkspaceDto
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, apierr.Upstream(err)
	}
	l.workspace = w.Id
	return l, nil
}

func readOK(resp *http.Response, err error) ([]byte, error) {
	if err != nil {
		return nil, apierr.Upstream(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, apierr.Upstream(err)
	}
	if e := apierr.FromResponse(resp, body); e != nil {
		return nil, e
	}
	return body, nil
}

type feedEvent struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	OccurredAt string          `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

// poll forwards the next page of the feed, oldest first, and reports whether the page was full.
func (l *listener) poll(ctx context.Context) (bool, error) {
	params := &api.V2ListEventsParams{Limit: ptr(int32(listenPage)), Types: optional(l.types)}
	if l.cursor != "" {
		params.Cursor = &l.cursor
	} else {
		params.Since = &l.since
	}
	body, err := readOK(l.api.V2ListEvents(ctx, params))
	if err != nil {
		return false, err
	}
	var page struct {
		Data       []feedEvent `json:"data"`
		NextCursor *string     `json:"nextCursor"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return false, apierr.Upstream(err)
	}
	for _, e := range page.Data {
		l.deliver(ctx, e)
	}
	if page.NextCursor != nil {
		l.cursor = *page.NextCursor
	}
	return len(page.Data) == listenPage, nil
}

// deliver posts one event, retrying once on a network error or 5xx, and prints the outcome.
func (l *listener) deliver(ctx context.Context, e feedEvent) {
	body, err := webhookBody(e, l.workspace)
	status := ""
	for attempt := 0; err == nil; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.target.String(), bytes.NewReader(body))
		for k, v := range signWebhook(l.target, body, e.ID, l.secret, time.Now()) {
			req.Header.Set(k, v)
		}
		resp, doErr := l.out.Do(req)
		if ctx.Err() != nil {
			return
		}
		if doErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			status = strconv.Itoa(resp.StatusCode)
		} else {
			status = doErr.Error()
		}
		if (doErr == nil && resp.StatusCode < 500) || attempt == 1 {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		status = err.Error()
	}
	fmt.Fprintf(l.app.Out, "%s  %s  %s  -> %s\n", output.Clean(e.OccurredAt), output.Clean(e.Type), output.Clean(e.ID), output.Clean(status))
}

// webhookBody is the v2 delivery body: the feed's event plus apiVersion and workspaceId, in Connect's field order.
func webhookBody(e feedEvent, workspace string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep data exactly as the API wrote it
	err := enc.Encode(struct {
		ID          string          `json:"id"`
		Type        string          `json:"type"`
		OccurredAt  string          `json:"occurredAt"`
		APIVersion  string          `json:"apiVersion"`
		WorkspaceID string          `json:"workspaceId"`
		Data        json.RawMessage `json:"data"`
	}{e.ID, e.Type, e.OccurredAt, config.APIVersion, workspace, e.Data})
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), err
}

// signWebhook returns the headers Connect sends with a v2 delivery: Content-Digest, Webhook-Id and an
// hmac-sha256 RFC 9421 signature labelled "sig" with keyid 1, created now and expiring 5 minutes later.
func signWebhook(u *url.URL, body []byte, eventID, secret string, now time.Time) map[string]string {
	sum := sha256.Sum256(body)
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	h := map[string]string{
		"Content-Type":   "application/json",
		"Content-Digest": "sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":",
		"Webhook-Id":     eventID + ".listen",
	}
	authority := strings.ToLower(u.Hostname())
	if strings.Contains(authority, ":") {
		authority = "[" + authority + "]"
	}
	if p := u.Port(); p != "" && !(u.Scheme == "http" && p == "80") && !(u.Scheme == "https" && p == "443") {
		authority += ":" + p
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	values := map[string]string{"@method": http.MethodPost, "@authority": authority, "@path": path, "@query": "?" + u.RawQuery,
		"content-digest": h["Content-Digest"], "content-type": h["Content-Type"], "webhook-id": h["Webhook-Id"]}
	created := now.Unix()
	params := sigParams(webhookComponents, created, created+300, base64.RawURLEncoding.EncodeToString(nonce), "1", "hmac-sha256")
	h["Signature-Input"] = "sig=" + params
	h["Signature"] = "sig=:" + hmacBase64([]byte(secret), signatureBase(webhookComponents, values, params)) + ":"
	return h
}

// sigParams is the RFC 9421 Signature-Input inner list; unset parameters are left out.
func sigParams(components []string, created, expires int64, nonce, keyID, alg string) string {
	quoted := make([]string, len(components))
	for i, c := range components {
		quoted[i] = `"` + c + `"`
	}
	s := "(" + strings.Join(quoted, " ") + ")"
	if created != 0 {
		s += ";created=" + strconv.FormatInt(created, 10)
	}
	if expires != 0 {
		s += ";expires=" + strconv.FormatInt(expires, 10)
	}
	for _, p := range [][2]string{{"nonce", nonce}, {"keyid", keyID}, {"alg", alg}} {
		if p[1] != "" {
			s += ";" + p[0] + `="` + p[1] + `"`
		}
	}
	return s
}

// signatureBase is the RFC 9421 section 2.5 signature base.
func signatureBase(components []string, values map[string]string, params string) string {
	var b strings.Builder
	for _, c := range components {
		b.WriteString(`"` + c + `": ` + values[c] + "\n")
	}
	b.WriteString(`"@signature-params": ` + params)
	return b.String()
}

func hmacBase64(key []byte, data string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}
