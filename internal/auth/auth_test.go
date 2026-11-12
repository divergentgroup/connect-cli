package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/divergentgroup/connect-cli/internal/creds"
)

func fakeJWT(ws string, scope string, exp time.Time) string {
	payload, _ := json.Marshal(map[string]any{"workspace_id": ws, "scope": scope, "exp": exp.Unix()})
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s"
}

// fakeIdP counts token exchanges and captures the last token request form.
type fakeIdP struct {
	*httptest.Server
	exchanges atomic.Int32
	lastForm  url.Values
}

func newIdP(t *testing.T) *fakeIdP {
	f := &fakeIdP{}
	mux := http.NewServeMux()
	mux.HandleFunc("/connect/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.lastForm = r.PostForm
		n := f.exchanges.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  fakeJWT("42", "connect:sms.read openid", time.Now().Add(time.Hour)),
			"refresh_token": fmt.Sprintf("rt-%d", n), "token_type": "Bearer", "expires_in": 3600,
		})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func TestLoopbackLoginIgnoresBadStateThenSucceeds(t *testing.T) {
	idp := newIdP(t)
	var authURL string
	done := make(chan struct{})
	go func() {
		defer close(done)
		p, err := Login(context.Background(), LoginOptions{Issuer: idp.URL, Out: &strings.Builder{}, OpenBrowser: func(u string) error {
			authURL = u
			go func() {
				q, _ := url.Parse(u)
				redirect := q.Query().Get("redirect_uri")
				state := q.Query().Get("state")
				bad, _ := http.Get(redirect + "?code=c&state=WRONG")
				if bad.StatusCode != 400 {
					t.Error("wrong state must be rejected")
				}
				alsoBad, _ := http.Get(strings.TrimSuffix(redirect, "/callback") + "/other?code=c&state=" + state)
				if alsoBad.StatusCode != 400 {
					t.Error("other paths must be rejected")
				}
				good, _ := http.Get(redirect + "?code=thecode&state=" + state)
				if good.StatusCode != 200 {
					t.Error("valid callback must succeed")
				}
			}()
			return nil
		}})
		if err != nil {
			t.Error(err)
			return
		}
		if p.WorkspaceID != "42" || p.RefreshToken != "rt-1" || len(p.Scopes) != 2 {
			t.Errorf("profile %+v", p)
		}
	}()
	<-done
	q, _ := url.Parse(authURL)
	v := q.Query()
	if v.Get("code_challenge_method") != "S256" || v.Get("resource") != "https://mcp.connect.ms/mcp" || v.Get("client_id") != "connect-cli" || !strings.Contains(v.Get("scope"), "offline_access") || !strings.HasPrefix(v.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Fatal(authURL)
	}
	if idp.lastForm.Get("code") != "thecode" || idp.lastForm.Get("code_verifier") == "" {
		t.Fatal(idp.lastForm)
	}
}

func TestLoopbackLoginEndsAtOnceWhenRefused(t *testing.T) {
	idp := newIdP(t)
	page := make(chan string, 1)
	_, err := Login(context.Background(), LoginOptions{Issuer: idp.URL, Out: &strings.Builder{}, Timeout: 10 * time.Second, OpenBrowser: func(u string) error {
		go func() {
			q, _ := url.Parse(u)
			resp, err := http.Get(q.Query().Get("redirect_uri") + "?error=access_denied&error_description=%3Cscript%3E&state=" + q.Query().Get("state"))
			var b []byte
			if err == nil {
				b, _ = io.ReadAll(resp.Body)
			}
			page <- string(b)
		}()
		return nil
	}})
	body := <-page
	if err == nil || err.Error() != "authorization refused: access_denied" || !strings.Contains(body, "not completed") || strings.Contains(body, "script") || idp.exchanges.Load() != 0 {
		t.Fatal(err, body)
	}
}

func TestLoopbackLoginTimesOut(t *testing.T) {
	idp := newIdP(t)
	_, err := Login(context.Background(), LoginOptions{Issuer: idp.URL, Out: &strings.Builder{}, Timeout: 100 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatal(err)
	}
}

func TestNoBrowserPasteFlow(t *testing.T) {
	idp := newIdP(t)
	var out strings.Builder
	pr, pw := newPipe()
	go func() {
		for !strings.Contains(out.String(), "Redirect URL:") {
			time.Sleep(5 * time.Millisecond)
		}
		u, _ := url.Parse(regexp.MustCompile(`https?://\S+/connect/authorize\S*`).FindString(out.String()))
		_, _ = fmt.Fprintf(pw, "http://127.0.0.1/callback?code=pasted&state=%s\n", u.Query().Get("state"))
	}()
	p, err := Login(context.Background(), LoginOptions{Issuer: idp.URL, NoBrowser: true, In: pr, Out: &out})
	if err != nil || p.WorkspaceID != "42" || idp.lastForm.Get("code") != "pasted" {
		t.Fatal(err, idp.lastForm)
	}
}

func TestRefreshIsSingleFlightAcrossGoroutines(t *testing.T) {
	idp := newIdP(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := creds.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	stale := &creds.Profile{Name: "acme", Kind: creds.KindToken, AccessToken: "old", RefreshToken: "rt-0", ExpiresAt: time.Now().Add(-time.Minute)}
	_ = store.Put(stale)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := Refresh(context.Background(), store, stale, idp.URL)
			if err != nil || p.RefreshToken != "rt-1" {
				t.Error(p, err)
			}
		}()
	}
	wg.Wait()
	if idp.exchanges.Load() != 1 {
		t.Fatalf("expected exactly one exchange, got %d", idp.exchanges.Load())
	}
	if idp.lastForm.Get("client_id") != "connect-cli" || idp.lastForm.Get("grant_type") != "refresh_token" {
		t.Fatal(idp.lastForm)
	}
}

func TestRefreshSkipsFreshToken(t *testing.T) {
	idp := newIdP(t)
	p := &creds.Profile{Name: "a", Kind: creds.KindToken, ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := Refresh(context.Background(), nil, p, idp.URL); err != nil || idp.exchanges.Load() != 0 {
		t.Fatal(err)
	}
}

func TestRevokePostsForm(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
	}))
	defer srv.Close()
	if err := Revoke(context.Background(), srv.URL, "rt"); err != nil || form.Get("token") != "rt" || form.Get("token_type_hint") != "refresh_token" {
		t.Fatal(err, form)
	}
}
