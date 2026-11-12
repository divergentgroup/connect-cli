package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/divergentgroup/connect-cli/internal/creds"
)

// RFC 9421 Appendix B.2.5, the vector Connect's own signer is tested against.
func TestSignatureMatchesRFC9421B25(t *testing.T) {
	key, _ := base64.StdEncoding.DecodeString("uzvJfB4u3N0Jy4T7NZ75MDVcr8zSTInedJtkgcu46YW4XByzNJjxBdtjUkdJPBtbmHhIDi6pcl8jsasjlTMtDQ==")
	components := []string{"date", "@authority", "content-type"}
	params := sigParams(components, 1618884473, 0, "", "test-shared-secret", "")
	if params != `("date" "@authority" "content-type");created=1618884473;keyid="test-shared-secret"` {
		t.Fatal(params)
	}
	base := signatureBase(components, map[string]string{"date": "Tue, 20 Apr 2021 02:07:55 GMT", "@authority": "example.com", "content-type": "application/json"}, params)
	if got := hmacBase64(key, base); got != "pxcQw6G3AjtMBQjwo8XzkZf/bws5LelbaMk5rGIGtE8=" {
		t.Fatal(got)
	}
}

func TestSignWebhookCoversWhatConnectSigns(t *testing.T) {
	u, _ := url.Parse("http://LocalHost:8443/in?x=1")
	body := []byte(`{"id":"e1"}`)
	h := signWebhook(u, body, "e1", "s3cret", time.Unix(1700000000, 0))
	sum := sha256.Sum256(body)
	if h["Content-Digest"] != "sha-256=:"+base64.StdEncoding.EncodeToString(sum[:])+":" || h["Webhook-Id"] != "e1.listen" || h["Content-Type"] != "application/json" {
		t.Fatal(h)
	}
	verifyListenSignature(t, h, "s3cret", "localhost:8443", "/in", "?x=1")
	if !strings.Contains(h["Signature-Input"], `;created=1700000000;expires=1700000300;nonce="`) || !strings.HasSuffix(h["Signature-Input"], `;keyid="1";alg="hmac-sha256"`) {
		t.Fatal(h["Signature-Input"])
	}
}

// verifyListenSignature rebuilds the signature base from the received headers, as a receiver would.
func verifyListenSignature(t *testing.T, h map[string]string, secret, authority, path, query string) {
	t.Helper()
	params := strings.TrimPrefix(h["Signature-Input"], "sig=")
	if !strings.HasPrefix(params, `("@method" "@authority" "@path" "@query" "content-digest" "content-type" "webhook-id");`) {
		t.Fatal(params)
	}
	base := signatureBase(webhookComponents, map[string]string{"@method": "POST", "@authority": authority, "@path": path, "@query": query,
		"content-digest": h["Content-Digest"], "content-type": h["Content-Type"], "webhook-id": h["Webhook-Id"]}, params)
	if want := "sig=:" + hmacBase64([]byte(secret), base) + ":"; h["Signature"] != want {
		t.Fatalf("signature %s, want %s", h["Signature"], want)
	}
}

func TestListenForwardsNewEventsInOrder(t *testing.T) {
	var mu sync.Mutex
	var queries []url.Values
	pages := map[string]string{
		"":   `{"data":[{"id":"e1","type":"sms.delivered","occurredAt":"2026-11-12T10:00:00.123Z","data":{"id":"m1","content":"<b>hi</b>"}}],"nextCursor":"c1"}`,
		"c1": `{"data":[{"id":"e2","type":"sms.failed","occurredAt":"2026-11-12T10:00:01Z","data":{"id":"m2"}},{"id":"e3","type":"sms.delivered","occurredAt":"2026-11-12T10:00:02Z","data":null}],"nextCursor":"c2"}`,
		"c2": `{"data":[],"nextCursor":"c2"}`,
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/workspace":
			_, _ = io.WriteString(w, `{"id":"ws1","name":"Acme","live":false,"apiOnlyMode":false,"sending":{"blocked":false},"country":"GB","createdAt":"2026-01-01T00:00:00Z"}`)
		case "/v2/events":
			mu.Lock()
			queries = append(queries, r.URL.Query())
			mu.Unlock()
			_, _ = io.WriteString(w, pages[r.URL.Query().Get("cursor")])
		default:
			w.WriteHeader(404)
		}
	}))
	defer api.Close()

	type received struct {
		body string
		hdr  map[string]string
	}
	var got []received
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h := map[string]string{}
		for _, k := range []string{"Content-Type", "Content-Digest", "Webhook-Id", "Signature-Input", "Signature"} {
			h[k] = r.Header.Get(k)
		}
		got = append(got, received{string(b), h})
		w.WriteHeader(204)
	}))
	defer local.Close()

	app, out, _ := newTestApp()
	app.APIURL = api.URL
	app.Store = creds.NewFileStore(filepath.Join(t.TempDir(), "c.json"))
	_ = app.Store.Put(&creds.Profile{Name: "acme", Kind: creds.KindAPIKey, APIKey: "secret-key", WorkspaceName: "Acme"})
	t.Setenv("CONNECT_API_KEY", "")
	target, _ := url.Parse(local.URL + "/hooks")
	start := time.Now()
	l, err := newListener(t.Context(), app, target, "s3cret", "sms.delivered,sms.failed")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := l.poll(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	// Starts at now, then follows the cursor; the type filter goes to the API.
	since, _ := time.Parse(time.RFC3339Nano, queries[0].Get("since"))
	if queries[0].Has("cursor") || since.Before(start.Add(-time.Second)) || queries[1].Get("cursor") != "c1" || queries[2].Get("cursor") != "c2" {
		t.Fatal(queries)
	}
	if queries[0].Get("types") != "sms.delivered,sms.failed" {
		t.Fatal(queries[0])
	}
	if len(got) != 3 || !strings.Contains(got[1].body, `"id":"e2"`) || !strings.Contains(got[2].body, `"id":"e3"`) {
		t.Fatal(got)
	}
	want := `{"id":"e1","type":"sms.delivered","occurredAt":"2026-11-12T10:00:00.123Z","apiVersion":"2026-10-01","workspaceId":"ws1","data":{"id":"m1","content":"<b>hi</b>"}}`
	if got[0].body != want {
		t.Fatalf("body\n%s\nwant\n%s", got[0].body, want)
	}
	if !strings.HasSuffix(got[2].body, `"data":null}`) || got[0].hdr["Webhook-Id"] != "e1.listen" {
		t.Fatal(got[2].body, got[0].hdr)
	}
	sum := sha256.Sum256([]byte(got[0].body))
	if got[0].hdr["Content-Digest"] != "sha-256=:"+base64.StdEncoding.EncodeToString(sum[:])+":" {
		t.Fatal(got[0].hdr)
	}
	verifyListenSignature(t, got[0].hdr, "s3cret", strings.TrimPrefix(local.URL, "http://"), "/hooks", "?")
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 3 || !strings.Contains(lines[0], "sms.delivered  e1  -> 204") {
		t.Fatal(out.String())
	}
}

func TestListenRefusesBadFlags(t *testing.T) {
	for _, args := range [][]string{{"listen"}, {"listen", "--forward-to", "http://example.com/hooks"}, {"listen", "--forward-to", "http://localhost:3000", "--interval", "100ms"}} {
		app, _, errb := newTestApp()
		if code := run(NewRoot(app), app, args); code != 2 {
			t.Fatalf("%v: exit %d %s", args, code, errb.String())
		}
	}
}
