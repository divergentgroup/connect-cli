package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/divergentgroup/connect-cli/internal/creds"
)

const summaryBody = `{"id":"42","name":"Acme Ltd","live":false,"sending":{"blocked":true,"reason":"billing"},"country":"GB","timeZone":"Europe/London","defaultSender":"Acme","apiOnlyMode":false,"createdAt":"2026-09-05T10:00:00Z"}`

func TestLoginWithAPIKeyFromStdinNeverEchoesKey(t *testing.T) {
	s := newStub(t, map[string]stubResp{"GET /v2/workspace": {200, summaryBody, nil}})
	app, out, errb := newTestApp()
	app.In = strings.NewReader("sk-verysecret\n")
	app.APIURL = s.URL
	app.Store = creds.NewFileStore(filepath.Join(t.TempDir(), "c.json"))
	t.Setenv("CONNECT_API_KEY", "")
	if code := run(NewRoot(app), app, []string{"auth", "login", "--api-key"}); code != 0 {
		t.Fatal(code, errb.String())
	}
	if strings.Contains(out.String()+errb.String(), "verysecret") {
		t.Fatal("key echoed")
	}
	if s.last.Header.Get("Authorization") != "Bearer sk-verysecret" || s.last.Header.Get("X-Api-Key") != "" {
		t.Fatal(s.last.Header)
	}
	p, err := app.Store.Get("acme-ltd")
	if err != nil || p.Kind != creds.KindAPIKey || p.WorkspaceName != "Acme Ltd" {
		t.Fatal(p, err)
	}
	if def, _ := app.Store.Default(); def != "acme-ltd" {
		t.Fatal(def)
	}
}

func TestStatusShowsEnvSource(t *testing.T) {
	app, out, _ := newTestApp()
	app.Store = creds.NewFileStore(filepath.Join(t.TempDir(), "c.json"))
	t.Setenv("CONNECT_API_KEY", "k")
	if code := run(NewRoot(app), app, []string{"auth", "status"}); code != 0 || !strings.Contains(out.String(), "env CONNECT_API_KEY") || strings.Contains(out.String(), "k\n") {
		t.Fatal(code, out.String())
	}
}

func TestWorkspaceShowJSONAndHuman(t *testing.T) {
	s := newStub(t, map[string]stubResp{"GET /v2/workspace": {200, summaryBody, nil}})
	_, do := testAppWithKey(t, s)
	code, out, _ := do("workspace", "show", "--json")
	if code != 0 || !strings.Contains(out, `"name": "Acme Ltd"`) {
		t.Fatal(code, out)
	}
	code, out, _ = do("workspace", "show")
	if code != 0 || !strings.Contains(out, "test") || !strings.Contains(out, "blocked (billing)") || !strings.Contains(out, "Europe/London") {
		t.Fatal(code, out)
	}
}

func TestNoCredentialsExitsThree(t *testing.T) {
	app, _, errb := newTestApp()
	app.Store = creds.NewFileStore(filepath.Join(t.TempDir(), "c.json"))
	t.Setenv("CONNECT_API_KEY", "")
	if code := run(NewRoot(app), app, []string{"workspace", "show"}); code != 3 || !strings.Contains(errb.String(), "auth login") {
		t.Fatal(code, errb.String())
	}
}

func TestLoginKeepsCredentialWhenSummaryEndpointIsMissing(t *testing.T) {
	s := newStub(t, map[string]stubResp{}) // every route 404s, like an API without v2
	app, out, errb := newTestApp()
	app.In = strings.NewReader("sk-verysecret\n")
	app.APIURL = s.URL
	app.Store = creds.NewFileStore(filepath.Join(t.TempDir(), "c.json"))
	t.Setenv("CONNECT_API_KEY", "")
	if code := run(NewRoot(app), app, []string{"auth", "login", "--api-key", "--profile", "acme"}); code != 0 {
		t.Fatal(code, errb.String())
	}
	if !strings.Contains(errb.String(), "GET /v2/workspace returned 404") || strings.Contains(out.String()+errb.String(), "verysecret") {
		t.Fatal(out.String(), errb.String())
	}
	if p, err := app.Store.Get("acme"); err != nil || p.APIKey != "sk-verysecret" {
		t.Fatal(p, err)
	}
}

type fakeIdp struct {
	*httptest.Server
	paths []string
}

// newFakeIdp serves canned bodies by path; the token endpoint returns an unsigned token.
func newFakeIdp(t *testing.T, routes map[string][]string) *fakeIdp {
	f := &fakeIdp{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.paths = append(f.paths, r.URL.Path)
		if r.URL.Path == "/connect/token" {
			io.WriteString(w, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"token_type":"Bearer","scope":"connect:sms.send"}`)
			return
		}
		q := routes[r.URL.Path]
		if len(q) == 0 {
			w.WriteHeader(500)
			io.WriteString(w, `{"next":"restart","message":"boom"}`)
			return
		}
		routes[r.URL.Path] = q[1:]
		code, body, _ := strings.Cut(q[0], " ")
		n, _ := strconv.Atoi(code)
		w.WriteHeader(n)
		io.WriteString(w, body)
	}))
	t.Cleanup(f.Close)
	return f
}

func signupApp(t *testing.T, idp *fakeIdp, api *stub) (*App, func(args ...string) (int, string, string), string) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("CONNECT_API_KEY", "")
	app, out, errb := newTestApp()
	app.Issuer = idp.URL
	app.APIURL = api.URL
	app.Store = creds.NewFileStore(filepath.Join(t.TempDir(), "c.json"))
	return app, func(args ...string) (int, string, string) {
		out.Reset()
		errb.Reset()
		app.JSON = false
		code := run(NewRoot(app), app, args)
		return code, out.String(), errb.String()
	}, filepath.Join(dir, "connect", "signup.json")
}

var startFlags = []string{"auth", "signup", "--name", "A", "--email", "a@x.io", "--phone", "+447700900000", "--workspace", "Acme", "--agree-terms"}

func TestSignupFlowToDoneStoresProfileAndRemovesState(t *testing.T) {
	idp := newFakeIdp(t, map[string][]string{
		"/signup/agent/start":   {`200 {"signupId":"s1","next":"email_code","message":"Enter the email code"}`},
		"/signup/agent/s1/code": {`200 {"signupId":"s1","next":"phone_code","message":"Now the phone code"}`, `200 {"signupId":"s1","next":"done","message":"Welcome","credential":"cred1"}`},
	})
	api := newStub(t, map[string]stubResp{"GET /v2/workspace": {200, summaryBody, nil}})
	app, do, state := signupApp(t, idp, api)
	code, out, errb := do(startFlags...)
	if code != 0 || !strings.Contains(out, "Enter the email code") {
		t.Fatal(code, out, errb)
	}
	fi, err := os.Stat(state)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatal(err, fi)
	}
	// no flags with state present: hint, no new sign-up
	if code, out, errb = do("auth", "signup"); code != 0 || !strings.Contains(out+errb, "verify CODE") {
		t.Fatal(code, out, errb)
	}
	if code, out, _ = do("auth", "signup", "verify", "111111"); code != 0 || !strings.Contains(out, "Now the phone code") {
		t.Fatal(code, out)
	}
	code, out, errb = do("auth", "signup", "verify", "222222")
	if code != 0 || !strings.Contains(out, "Welcome") || strings.Contains(out+errb, "cred1") {
		t.Fatal(code, out, errb)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state kept", err)
	}
	p, err := app.Store.Get("acme-ltd")
	if err != nil || p.Kind != creds.KindToken || p.AccessToken != "at" || p.WorkspaceName != "Acme Ltd" {
		t.Fatal(p, err)
	}
}

func TestSignupBrowserRemovesStateAndExitsZero(t *testing.T) {
	idp := newFakeIdp(t, map[string][]string{
		"/signup/agent/start":   {`200 {"signupId":"s1","next":"email_code","message":"m"}`},
		"/signup/agent/s1/code": {`200 {"signupId":"s1","next":"browser","message":"Finish in your browser"}`},
	})
	app, do, state := signupApp(t, idp, newStub(t, nil))
	_ = app
	do(startFlags...)
	code, out, _ := do("auth", "signup", "verify", "1")
	if code != 0 || !strings.Contains(out, "Finish in your browser") {
		t.Fatal(code, out)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state kept")
	}
}

func TestSignup404RemovesStateAndErrors400KeepsIt(t *testing.T) {
	idp := newFakeIdp(t, map[string][]string{
		"/signup/agent/start":   {`200 {"signupId":"s1","next":"email_code","message":"m"}`},
		"/signup/agent/s1/code": {`400 {"signupId":"s1","next":"email_code","message":"Wrong code"}`, `404 {"next":"restart","message":"Unknown or expired"}`},
	})
	_, do, state := signupApp(t, idp, newStub(t, nil))
	do(startFlags...)
	code, _, errb := do("auth", "signup", "verify", "1")
	if code != 4 || !strings.Contains(errb, "Wrong code") {
		t.Fatal(code, errb)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("state lost on 400")
	}
	code, _, errb = do("auth", "signup", "verify", "2")
	if code != 4 || !strings.Contains(errb, "Unknown or expired") {
		t.Fatal(code, errb)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state kept on 404")
	}
}

func TestSignupResendSolvesPowAndJSONHidesCredential(t *testing.T) {
	idp := newFakeIdp(t, map[string][]string{
		"/signup/agent/start":     {`200 {"signupId":"s1","next":"email_code","message":"m"}`},
		"/signup/agent/s1/resend": {`200 {"next":"pow","message":"work","powChallenge":"c","powDifficulty":4}`, `200 {"signupId":"s1","next":"email_code","message":"Sent again"}`},
	})
	_, do, _ := signupApp(t, idp, newStub(t, nil))
	do(startFlags...)
	code, out, errb := do("auth", "signup", "resend", "--json")
	if code != 0 || !strings.Contains(out, `"next"`) || !strings.Contains(out, "Sent again") {
		t.Fatal(code, out, errb)
	}
}
