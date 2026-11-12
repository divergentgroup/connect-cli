package apierr

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func problem(code, detail string) string {
	return `{"type":"https://api.connect.ms/problems/` + code + `","title":"T","status":0,"detail":"` + detail + `","requestId":"req-1"}`
}

func TestFromResponseMapsProblemBodies(t *testing.T) {
	cases := []struct {
		status int
		body   string
		hdr    http.Header
		code   string
		exit   int
	}{
		{403, problem("forbidden", "needs live"), http.Header{"Www-Authenticate": {`Bearer error="insufficient_scope", scope="connect:sms.send.live connect:sms.send"`}}, "insufficient_scope", 3},
		{403, problem("forbidden", "bypassSuppressions cannot be set"), http.Header{"Www-Authenticate": {`Bearer error="forbidden_field"`}}, "forbidden_field", 3},
		{403, problem("sending_blocked", "Sending is currently disabled."), nil, "sending_blocked", 3},
		{401, problem("unauthenticated", "expired"), nil, "invalid_token", 3},
		{401, problem("signature_required", "x"), nil, "strict_api_mode", 3},
		{401, problem("replayed_request", "x"), nil, "strict_api_mode", 3},
		{400, `{"type":"https://api.connect.ms/problems/validation_failed","title":"The request is not valid.","detail":"recipient: bad","errors":{"recipient":["bad"]}}`, nil, "validation", 4},
		{409, problem("idempotency_key_reused", "x"), nil, "idempotency_key_reused", 4},
		{404, ``, nil, "not_found", 4},
		{429, problem("rate_limited", "slow down"), http.Header{"Retry-After": {"7"}}, "rate_limited", 5},
		{429, problem("api_key_limit", "daily limit"), nil, "api_key_limit", 5},
		{402, problem("payment_required", "Required top-up: 12.50 GBP."), nil, "payment_required", 6},
		{500, ``, http.Header{"X-Request-Id": {"abc"}}, "upstream", 5},
		{503, ``, nil, "unavailable", 5},
	}
	for _, c := range cases {
		resp := &http.Response{StatusCode: c.status, Header: c.hdr}
		if resp.Header == nil {
			resp.Header = http.Header{}
		}
		e := FromResponse(resp, []byte(c.body))
		if e.Code != c.code || e.ExitCode() != c.exit {
			t.Errorf("%d %s: got %s/%d want %s/%d", c.status, c.body, e.Code, e.ExitCode(), c.code, c.exit)
		}
	}
	if FromResponse(&http.Response{StatusCode: 207, Header: http.Header{}}, nil) != nil {
		t.Error("207 is success")
	}
}

func TestProblemDetailsAreCarried(t *testing.T) {
	e := FromResponse(&http.Response{StatusCode: 403, Header: http.Header{"Www-Authenticate": {`Bearer error="insufficient_scope", scope="connect:sms.send.live"`}}},
		[]byte(problem("forbidden", "This action needs the connect:sms.send.live scope.")))
	if e.Message != "This action needs the connect:sms.send.live scope." || e.RequestID != "req-1" || len(e.Required) != 1 || e.Required[0] != "connect:sms.send.live" {
		t.Fatalf("%+v", e)
	}
	var out, errb bytes.Buffer
	e.Print(&out, &errb, false)
	if !strings.Contains(errb.String(), "Required scope: connect:sms.send.live") {
		t.Fatal(errb.String())
	}
	e = FromResponse(&http.Response{StatusCode: 401, Header: http.Header{}}, []byte(problem("signature_invalid", "bad signature")))
	if !strings.Contains(e.Message, "Strict API Mode") || !strings.Contains(e.Message, "cannot sign") {
		t.Fatal(e.Message)
	}
	e = FromResponse(&http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"7"}}}, []byte(problem("rate_limited", "")))
	if e.RetryAfterSeconds != 7 || e.Message != "T" {
		t.Fatalf("%+v", e)
	}
}

func TestFieldErrorsAreNeverCollapsed(t *testing.T) {
	e := FromResponse(&http.Response{StatusCode: 400, Header: http.Header{}},
		[]byte(`{"type":"https://api.connect.ms/problems/validation_failed","title":"The request is not valid.","detail":"name: too long name: reserved","errors":{"name":["too long","reserved"]}}`))
	var out, errb bytes.Buffer
	e.Print(&out, &errb, false)
	if errb.String() != "The request is not valid.\nname: too long\nname: reserved\n" {
		t.Fatal(errb.String())
	}
	if out.Len() != 0 {
		t.Fatal("stdout must stay empty without --json")
	}
	e.Print(&out, &errb, true)
	var back Error
	if json.Unmarshal(out.Bytes(), &back) != nil || back.FieldErrors["name"][1] != "reserved" {
		t.Fatal(out.String())
	}
}

func TestPrintStripsTerminalEscapes(t *testing.T) {
	var out, errb strings.Builder
	(&Error{Code: "validation", Message: "bad\x1b]0;pwned\x07", FieldErrors: map[string][]string{"to\x1b[2J": {"no\x07"}}}).Print(&out, &errb, false)
	if strings.ContainsAny(errb.String(), "\x1b\x07") || !strings.Contains(errb.String(), "bad\nto: no\n") {
		t.Fatalf("%q", errb.String())
	}
}
