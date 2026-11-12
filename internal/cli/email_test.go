package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/divergentgroup/connect-cli/internal/creds"
)

const emailBody = `{"id":"e1","sender":"hi@acme.test","recipient":"ann@example.com","subject":"Hello","status":"delivered","kind":"transactional","openCount":2,
"content":{"text":"Hi Ann"},"createdAt":"2026-11-12T10:00:00Z","updatedAt":"2026-11-12T10:00:05Z","deliveredAt":"2026-11-12T10:00:05Z"}`

const domainBody = `{"id":"d1","domain":"mail.acme.test","status":"pending","needsUpgrade":false,"createdAt":"2026-11-12T10:00:00Z",
"records":[{"type":"TXT","name":"lm._domainkey.mail.acme.test","value":"v=DKIM1; p=abc","purpose":"dkim","required":true,"status":"pending"}]}`

const emailTemplateBody = `{"id":"t1","name":"welcome","format":"html","html":"<p>Hi {{name}}</p>","text":"Hi {{name}}","createdAt":"2026-11-12T10:00:00Z","updatedAt":"2026-11-12T10:00:00Z"}`

func TestEmailCommandsRequestShape(t *testing.T) {
	dir := t.TempDir()
	html := filepath.Join(dir, "body.html")
	_ = os.WriteFile(html, []byte("From a file"), 0o600)
	pdf := filepath.Join(dir, "invoice.pdf")
	_ = os.WriteFile(pdf, []byte("PDF"), 0o600)
	list := func(item string) string { return `{"data":[` + item + `],"nextCursor":null}` }
	cases := []struct {
		args  []string
		route string
		resp  string
		body  []string // fragments the JSON body must contain
		query string   // the raw query the request must carry
		out   string   // a fragment stdout must contain
	}{
		{[]string{"email", "send", "--to", "ann@example.com", "--from", "hi@acme.test", "--subject", "Hello", "--html-file", html, "--text", "Hi", "--kind", "marketing",
			"--client-ref", "r1", "--track-opens", "--attach", pdf}, "POST /v2/email", emailBody,
			[]string{`"recipient":"ann@example.com"`, `"sender":"hi@acme.test"`, `"subject":"Hello"`, `"content":{"html":"From a file","text":"Hi"}`, `"kind":"marketing"`,
				`"clientReference":"r1"`, `"trackOpens":true`, `"attachments":[{"content":"UERG","contentType":"application/pdf","filename":"invoice.pdf"}]`}, "", "e1\n"},
		{[]string{"email", "send", "--to", "ann@example.com", "--from", "hi@acme.test", "--subject", "Hi", "--template", "welcome", "--data", `{"name":"Ann"}`}, "POST /v2/email", emailBody,
			[]string{`"template":{"data":{"name":"Ann"},"name":"welcome"}`}, "", "e1"},
		{[]string{"email", "get", "e1"}, "GET /v2/email/e1", emailBody, nil, "", "Hi Ann"},
		{[]string{"email", "list", "--status", "delivered", "--kind", "transactional", "--to", "ann@example.com", "--client-tag", "promo", "--limit", "10"}, "GET /v2/email", list(emailBody), nil,
			"clientTag=promo&kind=transactional&limit=10&recipient=ann%40example.com&status=delivered", "e1\t"},
		{[]string{"email", "cancel", "e1"}, "DELETE /v2/email/e1", emailBody, nil, "", "Cancelled e1"},
		{[]string{"email", "events", "e1", "--cursor", "c1"}, "GET /v2/email/e1/events", list(`{"id":"v1","type":"email.delivered","occurredAt":"2026-11-12T10:00:05Z"}`), nil, "cursor=c1", "email.delivered"},
		{[]string{"email", "templates", "list"}, "GET /v2/email/templates", list(emailTemplateBody), nil, "", "welcome"},
		{[]string{"email", "templates", "get", "t1"}, "GET /v2/email/templates/t1", emailTemplateBody, nil, "", "HTML:\n<p>Hi {{name}}</p>"},
		{[]string{"email", "templates", "create", "--name", "welcome", "--html", "<p>Hi</p>", "--format", "MJML", "--sample-data", `{"name":"Ann"}`}, "POST /v2/email/templates", emailTemplateBody,
			[]string{`"format":"mjml"`, `"name":"welcome"`, `"sampleData":{"name":"Ann"}`}, "", "t1"},
		{[]string{"email", "templates", "update", "t1", "--description", "New"}, "PATCH /v2/email/templates/t1", emailTemplateBody, []string{`{"description":"New"}`}, "", "Email template updated"},
		{[]string{"email", "templates", "delete", "t1"}, "DELETE /v2/email/templates/t1", "", nil, "", "Email template deleted"},
		{[]string{"email", "templates", "render", "t1", "--data", `{"name":"Bo"}`}, "POST /v2/email/templates/t1/render", `{"html":"<p>Hi Bo</p>","text":"Hi Bo"}`,
			[]string{`{"data":{"name":"Bo"}}`}, "", "Text:\nHi Bo"},
		{[]string{"email", "domains", "list"}, "GET /v2/email/domains", list(domainBody), nil, "", "mail.acme.test"},
		{[]string{"email", "domains", "get", "d1"}, "GET /v2/email/domains/d1", domainBody, nil, "", "lm._domainkey.mail.acme.test"},
		{[]string{"email", "domains", "add", "mail.acme.test"}, "POST /v2/email/domains", domainBody, []string{`{"domain":"mail.acme.test"}`}, "", "v=DKIM1; p=abc"},
		{[]string{"email", "domains", "verify", "d1"}, "POST /v2/email/domains/d1/verify", domainBody, nil, "", "DNS records to publish"},
		{[]string{"email", "domains", "delete", "d1"}, "DELETE /v2/email/domains/d1", "", nil, "", "Domain removed"},
		{[]string{"email", "includes", "list"}, "GET /v2/email/templates/includes", list(`{"name":"header.mjml","content":"<mj-text/>"}`), nil, "", "header.mjml"},
		{[]string{"email", "includes", "get", "header.mjml"}, "GET /v2/email/templates/includes/header.mjml", `{"name":"header.mjml","content":"<mj-text/>"}`, nil, "", "<mj-text/>"},
		{[]string{"email", "includes", "create", "--name", "header.mjml", "--content", "mj-text"}, "POST /v2/email/templates/includes", `{"name":"header.mjml","content":"<mj-text/>"}`,
			[]string{`"name":"header.mjml"`, `"content":"mj-text"`}, "", "Include saved"},
		{[]string{"email", "includes", "delete", "header.mjml"}, "DELETE /v2/email/templates/includes/header.mjml", "", nil, "", "Include deleted"},
		{[]string{"email", "test-recipients", "list"}, "GET /v2/email/test-recipients", list(`{"id":"r1","email":"me@acme.test","verified":true,"createdAt":"2026-11-12T10:00:00Z"}`), nil, "", "me@acme.test"},
		{[]string{"email", "test-recipients", "add", "me@acme.test"}, "POST /v2/email/test-recipients", `{"id":"r1","email":"me@acme.test","verified":false,"createdAt":"2026-11-12T10:00:00Z"}`,
			[]string{`{"email":"me@acme.test"}`}, "", "r1"},
		{[]string{"email", "test-recipients", "delete", "r1"}, "DELETE /v2/email/test-recipients/r1", "", nil, "", "Test recipient removed"},
		{[]string{"reports", "email-deliverability", "--from", "2026-11-01", "--to", "2026-11-07"}, "GET /v2/reports/email/deliverability",
			`{"period":{"from":"2026-11-01","to":"2026-11-07"},"byKind":[{"kind":"marketing","stats":{"sent":10,"delivered":9,"hardBounced":1,"deliveryRate":0.9}}],"daily":[],"total":{"sent":10,"delivered":9,"hardBounced":1,"deliveryRate":0.9}}`,
			nil, "since=2026-11-01&until=2026-11-07", "90.0%"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args[:3], " "), func(t *testing.T) {
			status := 200
			if tc.resp == "" {
				status = 204
			} else if strings.HasPrefix(tc.route, "POST") && !strings.HasSuffix(tc.route, "/verify") && !strings.HasSuffix(tc.route, "/render") {
				status = 201
			}
			s := newStub(t, map[string]stubResp{tc.route: {status, tc.resp, nil}})
			_, do := testAppWithKey(t, s)
			code, out, errb := do(tc.args...)
			if code != 0 || !strings.Contains(out, tc.out) {
				t.Fatal(code, out, errb)
			}
			for _, want := range tc.body {
				if !strings.Contains(string(s.body), want) {
					t.Fatal(want, string(s.body))
				}
			}
			if tc.query != "" && s.last.URL.Query().Encode() != tc.query {
				t.Fatal(s.last.URL.RawQuery)
			}
		})
	}
}

func TestEmailSendIdempotencyKeyAndNoticeAndRequiredBody(t *testing.T) {
	s := newStub(t, map[string]stubResp{"POST /v2/email": {201, emailBody, nil}})
	_, do := testAppWithKey(t, s)
	code, _, errb := do("email", "send", "--to", "a@b.test", "--from", "hi@acme.test", "--subject", "S", "--text", "x", "--idempotency-key", "k1")
	if code != 0 || s.last.Header.Get("Idempotency-Key") != "k1" || !strings.Contains(errb, "Sending as") || strings.Contains(string(s.body), "bypassSuppressions") {
		t.Fatal(code, errb, s.last.Header)
	}
	s.last = nil
	if code, _, errb = do("email", "send", "--to", "a@b.test", "--from", "hi@acme.test", "--subject", "S"); code != 2 || s.last != nil || !strings.Contains(errb, "--template") {
		t.Fatal(code, errb)
	}
	if code, _, errb = do("email", "send", "--to", "a@b.test", "--from", "hi@acme.test", "--subject", "S", "--html", "x", "--html-file", "y"); code != 2 || !strings.Contains(errb, "not both") {
		t.Fatal(code, errb)
	}
}

func TestEmailSetupCommandsRefusedUnderToken(t *testing.T) {
	s := newStub(t, nil)
	app, do := testAppWithKey(t, s)
	_ = app.Store.Put(&creds.Profile{Name: "tok", Kind: creds.KindToken, AccessToken: "jwt", ExpiresAt: farFuture()})
	_ = app.Store.SetDefault("tok")
	for _, args := range [][]string{{"email", "domains", "list"}, {"email", "templates", "render", "t1"}, {"email", "includes", "list"}, {"email", "test-recipients", "list"}} {
		if code, _, errb := do(args...); code != 3 || !strings.Contains(errb, "--api-key") || s.last != nil {
			t.Fatal(args, code, errb)
		}
	}
}
