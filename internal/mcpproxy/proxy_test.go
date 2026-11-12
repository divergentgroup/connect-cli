package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProxyMirrorsToolsAndForwardsCalls(t *testing.T) {
	var seenKey string
	upstream := mcp.NewServer(&mcp.Implementation{Name: "connect-mcp", Version: "9"}, &mcp.ServerOptions{Instructions: "be nice"})
	schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
	upstream.AddTool(&mcp.Tool{Name: "echo", Description: "echoes", InputSchema: schema}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in struct{ Text string }
		_ = json.Unmarshal(req.Params.Arguments, &in)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}, IsError: in.Text == "fail"}, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return upstream }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("X-Api-Key")
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	ctx := t.Context()
	cred := &creds.Credential{Profile: &creds.Profile{Kind: creds.KindAPIKey, APIKey: "sk-test"}, Source: "test"}
	serverT, clientT := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, srv.URL, func(context.Context) (*creds.Credential, error) { return cred, nil }, nil, serverT)
	}()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	if init := cs.InitializeResult(); init.ServerInfo.Name != "connect-mcp" || init.Instructions != "be nice" {
		t.Fatalf("%+v", init)
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Fatal(tools, err)
	}
	got, _ := json.Marshal(tools.Tools[0].InputSchema)
	if !strings.Contains(string(got), `"required":["text"]`) {
		t.Fatal(string(got))
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "hi"}})
	if err != nil || res.IsError || res.Content[0].(*mcp.TextContent).Text != "hi" {
		t.Fatal(res, err)
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "fail"}})
	if err != nil || !res.IsError {
		t.Fatal(res, err)
	}
	if seenKey != "sk-test" {
		t.Fatal("upstream did not see the API key")
	}
	_ = cs.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRedirectNeverCarriesTheCredential(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Api-Key") + r.Header.Get("Authorization")
	}))
	defer other.Close()
	srv := httptest.NewServer(http.RedirectHandler(other.URL, http.StatusTemporaryRedirect))
	defer srv.Close()
	cred := &creds.Credential{Profile: &creds.Profile{Kind: creds.KindAPIKey, APIKey: "sk-test"}}
	resp, err := httpClient(func(context.Context) (*creds.Credential, error) { return cred, nil }, nil).Get(srv.URL)
	if err != nil || resp.StatusCode != http.StatusTemporaryRedirect || leaked != "" {
		t.Fatal(err, resp, leaked)
	}
}

func TestCredentialIsResolvedForEveryRequest(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = append(seen, r.Header.Get("Authorization")) }))
	defer srv.Close()
	n := 0
	c := httpClient(func(context.Context) (*creds.Credential, error) {
		n++
		return &creds.Credential{Profile: &creds.Profile{Kind: creds.KindToken, AccessToken: fmt.Sprint("t", n)}}, nil
	}, nil)
	for range 2 {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if strings.Join(seen, ",") != "Bearer t1,Bearer t2" {
		t.Fatal(seen)
	}
}
