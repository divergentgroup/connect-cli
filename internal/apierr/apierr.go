// Package apierr maps Connect v2 problem responses to the CLI error shape shared by stderr, --json and exit codes.
package apierr

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/output"
)

type Error struct {
	Code              string              `json:"error"`
	Required          []string            `json:"required,omitempty"`
	Message           string              `json:"message"`
	FieldErrors       map[string][]string `json:"fieldErrors"`
	RetryAfterSeconds int                 `json:"retryAfterSeconds,omitempty"`
	RequestID         string              `json:"requestId,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func (e *Error) ExitCode() int {
	switch e.Code {
	case "usage":
		return 2
	case "insufficient_scope", "invalid_token", "wrong_workspace", "forbidden_field", "forbidden", "strict_api_mode", "sending_blocked":
		return 3
	case "validation", "not_found", "conflict", "cursor_expired", "idempotency_key_required", "idempotency_key_reused", "idempotency_in_progress",
		"method_not_allowed", "payload_too_large", "unsupported_media_type", "unknown_api_version":
		return 4
	case "rate_limited", "api_key_limit", "upstream", "unavailable":
		return 5
	case "payment_required":
		return 6
	}
	return 1
}

func Usage(msg string) *Error { return &Error{Code: "usage", Message: msg} }
func Auth(msg string) *Error  { return &Error{Code: "invalid_token", Message: msg} }
func Upstream(err error) *Error {
	return &Error{Code: "upstream", Message: "Connect API unreachable: " + err.Error()}
}

const strictMessage = "This API key uses Strict API Mode, so every request must be signed, and connect cannot sign requests. " +
	"Use an API key without Strict API Mode, or sign in with `connect auth login`."

var challengeScope = regexp.MustCompile(`scope="([^"]*)"`)

// FromResponse maps a non-2xx v2 response (RFC 9457 problem+json) to an Error; nil for success.
func FromResponse(resp *http.Response, body []byte) *Error {
	if resp.StatusCode < 400 {
		return nil
	}
	var p struct {
		Type, Title, Detail, RequestID string
		Errors                         map[string][]string
	}
	_ = json.Unmarshal(body, &p)
	code := p.Type[strings.LastIndex(p.Type, "/")+1:] // https://api.connect.ms/problems/<code>
	e := &Error{Code: code, Message: p.Detail, FieldErrors: p.Errors, RequestID: p.RequestID}
	if e.Message == "" || len(p.Errors) > 0 { // a validation detail only repeats the field errors printed below it
		e.Message = p.Title
	}
	if e.RequestID == "" {
		e.RequestID = resp.Header.Get("X-Request-Id")
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	switch s := resp.StatusCode; {
	case s == 401 && (code == "signature_required" || code == "signature_invalid" || code == "replayed_request"):
		e.Code, e.Message = "strict_api_mode", strictMessage
	case s == 401:
		e.Code = "invalid_token"
	case s == 403 && strings.Contains(challenge, "insufficient_scope"):
		e.Code = "insufficient_scope"
		if m := challengeScope.FindStringSubmatch(challenge); m != nil {
			e.Required = strings.Fields(m[1])
		}
	case s == 403 && strings.Contains(challenge, "forbidden_field"):
		e.Code = "forbidden_field"
	case s == 403 && code == "":
		e.Code = "forbidden"
	case s == 402:
		e.Code = "payment_required"
	case s == 404:
		e.Code = "not_found"
	case s == 429:
		if code != "api_key_limit" {
			e.Code = "rate_limited"
		}
		e.RetryAfterSeconds, _ = strconv.Atoi(resp.Header.Get("Retry-After"))
	case s == 503:
		e.Code = "unavailable"
	case s >= 500:
		e.Code = "upstream"
	case s == 400 || code == "":
		e.Code = "validation"
	}
	if e.Message == "" {
		e.Message = http.StatusText(resp.StatusCode)
		if resp.Request != nil {
			e.Message = fmt.Sprintf("%s %s returned %d %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, http.StatusText(resp.StatusCode))
		}
	}
	return e
}

// Print writes the message (and field errors) to stderr and, under --json, the object to stdout.
func (e *Error) Print(stdout, stderr io.Writer, asJSON bool) {
	fmt.Fprintln(stderr, output.CleanText(e.Message))
	keys := make([]string, 0, len(e.FieldErrors))
	for k := range e.FieldErrors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, m := range e.FieldErrors[k] {
			fmt.Fprintf(stderr, "%s: %s\n", output.Clean(k), output.Clean(m))
		}
	}
	switch e.Code {
	case "insufficient_scope":
		if len(e.Required) > 0 {
			fmt.Fprintln(stderr, "Required scope: "+output.Clean(strings.Join(e.Required, " ")))
		}
		fmt.Fprintln(stderr, "Run `connect auth login` again and tick the missing permission.")
	case "invalid_token":
		fmt.Fprintln(stderr, "Run `connect auth login` to sign in.")
	case "payment_required":
		fmt.Fprintln(stderr, "Top up at "+config.BillingURL)
	case "rate_limited", "api_key_limit":
		if e.RetryAfterSeconds > 0 {
			fmt.Fprintf(stderr, "Retry after %d seconds.\n", e.RetryAfterSeconds)
		}
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(e)
	}
}
