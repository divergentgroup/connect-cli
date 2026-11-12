package cli

import (
	"path/filepath"
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
