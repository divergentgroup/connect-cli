// Package mcpproxy exposes the hosted Connect MCP server over a local transport,
// attaching the stored credential so agents never handle the key themselves.
package mcpproxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CredFunc returns the credential for a request, refreshing an expiring token first.
type CredFunc func(context.Context) (*creds.Credential, error)

// credTransport injects the current credential headers into every upstream request.
type credTransport struct {
	cred CredFunc
	base http.RoundTripper
}

func (t credTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cred, err := t.cred(req.Context())
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	if err := cred.Apply(req.Context(), req); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// httpClient attaches the credential and never follows redirects, so it cannot leak to another host.
func httpClient(cred CredFunc, base http.RoundTripper) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{Transport: credTransport{cred, base}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Proxy mirrors the upstream tool catalogue and forwards calls.
type Proxy struct {
	upstream string
	http     *http.Client
	session  *mcp.ClientSession
}

// New connects to the upstream server. base may be nil for http.DefaultTransport.
func New(ctx context.Context, upstream string, cred CredFunc, base http.RoundTripper) (*Proxy, error) {
	p := &Proxy{upstream: upstream, http: httpClient(cred, base)}
	return p, p.connect(ctx)
}

func (p *Proxy) connect(ctx context.Context) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "connect-cli", Version: config.Version}, nil)
	s, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: p.upstream, HTTPClient: p.http}, nil)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", p.upstream, err)
	}
	p.session = s
	return nil
}

// Server builds a local server whose tools are the upstream tools, one for one.
func (p *Proxy) Server(ctx context.Context) (*mcp.Server, error) {
	init := p.session.InitializeResult()
	impl := &mcp.Implementation{Name: "connect", Version: config.Version}
	if init.ServerInfo != nil {
		impl = &mcp.Implementation{Name: init.ServerInfo.Name, Version: init.ServerInfo.Version}
	}
	server := mcp.NewServer(impl, &mcp.ServerOptions{Instructions: init.Instructions})
	for t, err := range p.session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("listing upstream tools: %w", err)
		}
		name := t.Name
		server.AddTool(&mcp.Tool{Name: t.Name, Title: t.Title, Description: t.Description, InputSchema: t.InputSchema, OutputSchema: t.OutputSchema, Annotations: t.Annotations},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return p.call(ctx, &mcp.CallToolParams{Name: name, Arguments: req.Params.Arguments})
			})
	}
	return server, nil
}

// call forwards a tool call, reconnecting once if the upstream session dropped.
func (p *Proxy) call(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	res, err := p.session.CallTool(ctx, params)
	if err != nil && errors.Is(err, mcp.ErrConnectionClosed) && ctx.Err() == nil {
		if err = p.connect(ctx); err != nil {
			return nil, err
		}
		res, err = p.session.CallTool(ctx, params)
	}
	return res, err
}

// Close ends the upstream session.
func (p *Proxy) Close() error { return p.session.Close() }

// Run serves the upstream tools over t until the client disconnects or ctx ends.
func Run(ctx context.Context, upstream string, cred CredFunc, base http.RoundTripper, t mcp.Transport) error {
	p, err := New(ctx, upstream, cred, base)
	if err != nil {
		return err
	}
	defer p.Close()
	server, err := p.Server(ctx)
	if err != nil {
		return err
	}
	return server.Run(ctx, t)
}
