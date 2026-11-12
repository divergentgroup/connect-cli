package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/divergentgroup/connect-cli/internal/creds"
)

const messageBody = `{"id":"m1","direction":"outbound","sender":"Acme","recipient":"+447700900001","content":"hi","status":"failed","parts":1,"encoding":"gsm7",
"reason":{"category":"unreachable","code":"absent_subscriber","description":"The handset was off","retryable":true},"cost":{"gbp":"0.0350","eur":"0.0400"},
"createdAt":"2026-11-12T10:00:00Z","updatedAt":"2026-11-12T10:00:05Z","failedAt":"2026-11-12T10:00:05Z"}`

func TestSmsSendRequestShape(t *testing.T) {
	s := newStub(t, map[string]stubResp{"POST /v2/sms": {201, messageBody, nil}})
	_, do := testAppWithKey(t, s)
	code, out, errb := do("sms", "send", "--to", "+447700900001", "--from", "Acme", "--content", "hi", "--kind", "Transactional", "--client-ref", "r1", "--link-mode", "both")
	if code != 0 || out != "m1\n" || !strings.Contains(errb, "Sending as") {
		t.Fatal(code, out, errb)
	}
	var body map[string]any
	_ = json.Unmarshal(s.body, &body)
	if body["kind"] != "transactional" || body["recipient"] != "+447700900001" || body["clientReference"] != "r1" || body["link"].(map[string]any)["mode"] != "both" {
		t.Fatal(string(s.body))
	}
	if _, ok := body["bypassSuppressions"]; ok {
		t.Fatal("bypassSuppressions must never be sent")
	}
	h := s.last.Header
	if h.Get("Authorization") != "Bearer secret-key" || h.Get("X-Api-Key") != "" || h.Get("Connect-Version") != "2026-10-01" {
		t.Fatal(h)
	}
}

func TestBadEnumIsUsageError(t *testing.T) {
	s := newStub(t, nil)
	_, do := testAppWithKey(t, s)
	code, _, errb := do("sms", "send", "--to", "+447700900001", "--from", "Acme", "--content", "hi", "--kind", "promo")
	if code != 2 || !strings.Contains(errb, "transactional, marketing, authentication") || s.last != nil {
		t.Fatal(code, errb)
	}
}

func TestInsufficientScopeExitsThreeWithRequiredScope(t *testing.T) {
	s := newStub(t, map[string]stubResp{"POST /v2/sms": {403, problem("forbidden", "This action needs the connect:sms.send.live scope."),
		map[string]string{"WWW-Authenticate": `Bearer error="insufficient_scope", scope="connect:sms.send.live"`}}})
	_, do := testAppWithKey(t, s)
	code, out, errb := do("sms", "send", "--to", "+447700900009", "--from", "Acme", "--content", "hi", "--json")
	if code != 3 || !strings.Contains(errb, "needs the connect:sms.send.live scope") || !strings.Contains(errb, "Required scope: connect:sms.send.live") || !strings.Contains(errb, "connect auth login") {
		t.Fatal(code, errb)
	}
	if !strings.Contains(out, `"error":"insufficient_scope"`) || !strings.Contains(out, `"required":["connect:sms.send.live"]`) || !strings.Contains(out, `"requestId":"r1"`) {
		t.Fatal(out)
	}
}

func TestStrictAPIModeKeyIsExplained(t *testing.T) {
	s := newStub(t, map[string]stubResp{"GET /v2/workspace": {401, problem("signature_required", "This API key requires signed requests."), nil}})
	_, do := testAppWithKey(t, s)
	code, _, errb := do("workspace", "show")
	if code != 3 || !strings.Contains(errb, "Strict API Mode") || !strings.Contains(errb, "cannot sign") || strings.Contains(errb, "Run `connect auth login` to sign in.") {
		t.Fatal(code, errb)
	}
}

func TestValidationFieldErrorsPrintedPerField(t *testing.T) {
	s := newStub(t, map[string]stubResp{"POST /v2/sms": {400, `{"type":"https://api.connect.ms/problems/validation_failed","title":"The request is not valid.","detail":"x",
"errors":{"recipient":["not E.164"],"content":["required"]},"requestId":"r1"}`, nil}})
	_, do := testAppWithKey(t, s)
	code, out, errb := do("sms", "send", "--to", "x", "--from", "Acme", "--content", "hi", "--json")
	if code != 4 || !strings.Contains(errb, "The request is not valid.\ncontent: required\nrecipient: not E.164") || !strings.Contains(out, `"fieldErrors"`) {
		t.Fatal(code, out, errb)
	}
}

func TestSmsGetShowsV2Fields(t *testing.T) {
	s := newStub(t, map[string]stubResp{"GET /v2/sms/m1": {200, messageBody, nil}})
	_, do := testAppWithKey(t, s)
	code, out, _ := do("sms", "get", "m1")
	for _, want := range []string{"Sender", "Acme", "Recipient", "failed", "unreachable absent_subscriber: The handset was off (retryable: yes)", "£0.0350 / €0.0400", "2026-11-12T10:00:05Z"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatal(code, want, out)
		}
	}
	if strings.Contains(out, "Delivered") {
		t.Fatal("empty values must be left out", out)
	}
}

func TestSmsSearchCursorPaging(t *testing.T) {
	s := newStub(t, map[string]stubResp{"GET /v2/sms": {200, `{"data":[` + messageBody + `],"nextCursor":"c2"}`, nil}})
	_, do := testAppWithKey(t, s)
	code, out, errb := do("sms", "search", "--number", "+447700900001", "--status", "failed", "--limit", "10", "--cursor", "c1")
	if code != 0 || !strings.HasPrefix(out, "m1\t") || !strings.Contains(errb, "--cursor c2") {
		t.Fatal(code, out, errb)
	}
	q := s.last.URL.Query()
	if q.Get("conversationWith") != "+447700900001" || q.Get("status") != "failed" || q.Get("limit") != "10" || q.Get("cursor") != "c1" {
		t.Fatal(s.last.URL)
	}
	// The last page has no nextCursor, so there is nothing more to fetch.
	s.routes["GET /v2/sms"] = stubResp{200, `{"data":[],"nextCursor":null}`, nil}
	if code, _, errb = do("sms", "search"); code != 0 || strings.Contains(errb, "--cursor") {
		t.Fatal(code, errb)
	}
}

func TestSuppressionsRemoveRefusedUnderToken(t *testing.T) {
	s := newStub(t, nil)
	app, do := testAppWithKey(t, s)
	_ = app.Store.Put(&creds.Profile{Name: "tok", Kind: creds.KindToken, AccessToken: "jwt", ExpiresAt: farFuture()})
	_ = app.Store.SetDefault("tok")
	code, _, errb := do("suppressions", "remove", "+447700900001")
	if code != 3 || !strings.Contains(errb, "--api-key") || s.last != nil {
		t.Fatal(code, errb, s.last)
	}
}

func TestSecretsPrintedOnceAndNeverInList(t *testing.T) {
	s := newStub(t, map[string]stubResp{
		"POST /v2/api-keys": {201, `{"id":"k1","key":"sk-new","keyPrefix":"ck_","keyLast4":"wxyz","activeFrom":"2026-11-12T10:00:00Z","createdAt":"2026-11-12T10:00:00Z","limits":{"usedToday":0,"usedThisMonth":0},"strictMode":false}`, nil},
		"GET /v2/api-keys":  {200, `{"data":[{"id":"k1","key":"sk-live","keyPrefix":"ck_","keyLast4":"wxyz","activeFrom":"2026-11-12T10:00:00Z","createdAt":"2026-11-12T10:00:00Z","limits":{"usedToday":0,"usedThisMonth":0},"strictMode":false}],"nextCursor":null}`, nil},
	})
	_, do := testAppWithKey(t, s)
	code, out, errb := do("api-keys", "create", "--name", "ci")
	if code != 0 || !strings.Contains(out, "sk-new") || !strings.Contains(errb, "shown once") || !strings.Contains(string(s.body), `"name":"ci"`) {
		t.Fatal(code, out, errb)
	}
	code, out, _ = do("api-keys", "list")
	if code != 0 || strings.Contains(out, "sk-live") || !strings.Contains(out, "ck_...wxyz") {
		t.Fatal(code, out)
	}
}

func TestSendBulkChunksResumeAndIdempotencyKeys(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var calls []string
	s := newStub(t, map[string]stubResp{"POST /v2/sms/batches": {202, `{"id":"b","status":"queued","source":"api","counts":{"total":1},"skipped":{},"errors":[{"index":0,"detail":"bad number"}],"createdAt":"2026-11-12T10:00:00Z"}`, nil}})
	s.observe = func(method, path, key string) { calls = append(calls, key) }
	file := filepath.Join(t.TempDir(), "m.csv")
	_ = os.WriteFile(file, []byte("Recipient,Content\n+447700900001,a\n+447700900002,b\n"), 0o600)
	_, do := testAppWithKey(t, s)
	code, out, errb := do("sms", "send-bulk", "--file", file, "--from", "Acme")
	if code != 0 || !strings.Contains(out, "Accepted 1 of 2") || !strings.Contains(errb, "row 1: bad number") {
		t.Fatal(code, out, errb)
	}
	if len(calls) != 1 || !strings.HasSuffix(calls[0], "-0") || len(calls[0]) != 34 {
		t.Fatal(calls)
	}
	var body struct {
		Messages []map[string]any `json:"messages"`
		Defaults map[string]any   `json:"defaults"`
	}
	_ = json.Unmarshal(s.body, &body)
	if len(body.Messages) != 2 || body.Defaults["sender"] != "Acme" || body.Defaults["kind"] != "marketing" {
		t.Fatal(string(s.body))
	}
	// Rerun: the manifest says chunk 0 is done, so nothing is sent again.
	code, _, errb = do("sms", "send-bulk", "--file", file, "--strict")
	if code != 4 || len(calls) != 1 || !strings.Contains(errb, "Resuming") {
		t.Fatal(code, errb, calls)
	}
}

func TestSendListDryRunPreviewsTheSameBody(t *testing.T) {
	s := newStub(t, map[string]stubResp{"POST /v2/sms/batches/preview": {200, `{"recipients":120,"parts":130,"cost":{"gbp":"4.55"},"paymentRequired":false,"skipped":{"suppressed":3},"errors":[]}`, nil}})
	_, do := testAppWithKey(t, s)
	code, out, errb := do("sms", "send-list", "--list", "l1", "--exclude-list", "l2", "--from", "Acme", "--content", "Sale on", "--dry-run")
	if code != 0 || !strings.Contains(out, "120") || !strings.Contains(out, "£4.55") || !strings.Contains(out, "suppressed 3") || strings.Contains(errb, "Sending as") {
		t.Fatal(code, out, errb)
	}
	if !strings.Contains(string(s.body), `"list":{"excludeIds":["l2"],"ids":["l1"]}`) || !strings.Contains(string(s.body), `"kind":"marketing"`) {
		t.Fatal(string(s.body))
	}
}

func TestContactsUpsertSendsIdempotencyKey(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var keys []string
	s := newStub(t, map[string]stubResp{"POST /v2/contacts/batch": {200, `{"created":1,"updated":0,"errors":[],"truncations":[]}`, nil}})
	s.observe = func(_, _, key string) { keys = append(keys, key) }
	file := filepath.Join(t.TempDir(), "c.csv")
	_ = os.WriteFile(file, []byte("Mobile,First Name,Email,tags\n+447700900001,Ann,not-an-email,vip;new\n"), 0o600)
	_, do := testAppWithKey(t, s)
	code, out, errb := do("contacts", "upsert", "--file", file, "--map", "Mobile=phone")
	if code != 0 || !strings.Contains(out, "Accepted 1 of 1") || len(keys) != 1 || keys[0] == "" {
		t.Fatal(code, out, errb, keys)
	}
	if !strings.Contains(string(s.body), `"number":"+447700900001"`) || !strings.Contains(string(s.body), `"tags":["vip","new"]`) || !strings.Contains(string(s.body), `"email":"not-an-email"`) {
		t.Fatal(string(s.body))
	}
}

func TestAPIOverrideMustBeHTTPSAndIsAnnounced(t *testing.T) {
	s := newStub(t, map[string]stubResp{"GET /v2/workspace": {200, `{"id":"1","name":"Acme"}`, nil}})
	app, do := testAppWithKey(t, s)
	code, _, errb := do("workspace", "show")
	if code != 0 || strings.Count(errb, "Using API "+s.URL+"\n") != 1 {
		t.Fatal(code, errb)
	}
	app.APIURL = "http://api.example.com"
	if code, _, errb = do("workspace", "show"); code != 2 || !strings.Contains(errb, "must use https") {
		t.Fatal(code, errb)
	}
	if code, _, errb = do("mcp", "--upstream", "http://mcp.example.com/mcp"); code != 2 || !strings.Contains(errb, "MCP server") {
		t.Fatal(code, errb)
	}
}

func TestUnsafeProfileNameRefused(t *testing.T) {
	s := newStub(t, nil)
	_, do := testAppWithKey(t, s)
	if code, _, errb := do("--profile", "../../.ssh/x", "workspace", "show"); code != 2 || !strings.Contains(errb, "--profile") || s.last != nil {
		t.Fatal(code, errb)
	}
	t.Setenv("CONNECT_PROFILE", "../../.ssh/x")
	app, out, errb := newTestApp()
	if code := run(NewRoot(app), app, []string{"auth", "logout"}); code != 2 {
		t.Fatal(code, out, errb)
	}
}
