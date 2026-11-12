package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/divergentgroup/connect-cli/internal/apierr"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/spf13/cobra"
)

// stub is a recorded Connect: path+method → status/body, capturing the last request.
type stub struct {
	*httptest.Server
	routes map[string]stubResp
	last   *http.Request
	body   []byte
	// observe, when set, sees every request's method, path and Idempotency-Key.
	observe func(method, path, key string)
}
type stubResp struct {
	status int
	body   string
	hdr    map[string]string
}

func newStub(t *testing.T, routes map[string]stubResp) *stub {
	s := &stub{routes: routes}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.last = r
		s.body = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(s.body)
		key := r.Method + " " + r.URL.Path
		if s.observe != nil {
			s.observe(r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"))
		}
		resp, ok := routes[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		for k, v := range resp.hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func testAppWithKey(t *testing.T, s *stub) (*App, func(args ...string) (int, string, string)) {
	app, out, errb := newTestApp()
	app.APIURL = s.URL
	app.Store = creds.NewFileStore(filepath.Join(t.TempDir(), "c.json"))
	_ = app.Store.Put(&creds.Profile{Name: "acme", Kind: creds.KindAPIKey, APIKey: "secret-key", WorkspaceName: "Acme"})
	t.Setenv("CONNECT_API_KEY", "")
	return app, func(args ...string) (int, string, string) {
		out.Reset()
		errb.Reset()
		app.JSON = false
		code := run(NewRoot(app), app, args)
		return code, out.String(), errb.String()
	}
}

func problem(code, detail string) string {
	return `{"type":"https://api.connect.ms/problems/` + code + `","title":"T","detail":"` + detail + `","requestId":"r1"}`
}

func TestAPIKeyOnlyCommandRefusedUnderTokenProfile(t *testing.T) {
	app, _, _ := newTestApp()
	app.Store = creds.NewFileStore(filepath.Join(t.TempDir(), "c.json"))
	_ = app.Store.Put(&creds.Profile{Name: "acme", Kind: creds.KindToken, AccessToken: "jwt", ExpiresAt: farFuture()})
	t.Setenv("CONNECT_API_KEY", "")
	cmd := annotate(&cobra.Command{Use: "x"}, "Op", scopeAPIKeyOnly)
	cmd.SetContext(t.Context())
	_, _, err := app.client(cmd)
	ae, ok := err.(*apierr.Error)
	if !ok || ae.ExitCode() != 3 {
		t.Fatal(err)
	}
}

func TestFinishMapsErrorsAndJSON(t *testing.T) {
	s := newStub(t, map[string]stubResp{"GET /x": {403, problem("forbidden", "no"), map[string]string{"WWW-Authenticate": `Bearer error="insufficient_scope", scope="connect:sms.read"`}}})
	app, _, _ := newTestApp()
	resp, err := http.Get(s.URL + "/x")
	if e := app.finish(resp, err, nil); e == nil || e.(*apierr.Error).Code != "insufficient_scope" || e.(*apierr.Error).Required[0] != "connect:sms.read" {
		t.Fatal(e)
	}
	s.routes["GET /x"] = stubResp{200, `{"a":1}`, nil}
	app.JSON = true
	called := false
	resp, err = http.Get(s.URL + "/x")
	if e := app.finish(resp, err, func([]byte) error { called = true; return nil }); e != nil || called {
		t.Fatal(e, called)
	}
}
