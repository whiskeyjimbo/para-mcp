// Package remotevault implements ports.Vault over an MCP HTTP client connection
// to a remote paras server.
package remotevault

import (
	"context"
	"encoding/json"
	"fmt"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/whiskeyjimbo/para-mcp/internal/ctxutil"
)

const requestIDHeader = "X-PARA-Request-Id"

// WithRequestID stores a request ID in ctx for propagation to outbound calls.
func WithRequestID(ctx context.Context, id string) context.Context {
	return ctxutil.WithRequestID(ctx, id)
}

// RequestIDFromContext returns the request ID stored in ctx, or "".
func RequestIDFromContext(ctx context.Context) string {
	return ctxutil.RequestIDFromContext(ctx)
}

// mcpConn wraps a connected MCP HTTP client.
type mcpConn struct {
	c *mcpclient.Client
}

// newConn creates a new MCP streamable-HTTP client connected to baseURL.
// The request ID from ctx is forwarded as X-PARA-Request-Id on every call.
func newConn(baseURL string) (*mcpConn, error) {
	headerFunc := transport.HTTPHeaderFunc(func(ctx context.Context) map[string]string {
		if id := ctxutil.RequestIDFromContext(ctx); id != "" {
			return map[string]string{requestIDHeader: id}
		}
		return nil
	})
	c, err := mcpclient.NewStreamableHttpClient(baseURL, transport.WithHTTPHeaderFunc(headerFunc))
	if err != nil {
		return nil, fmt.Errorf("remotevault: create client for %q: %w", baseURL, err)
	}
	return &mcpConn{c: c}, nil
}

// initialize sends the MCP Initialize handshake. Must be called once after newConn.
func (c *mcpConn) initialize(ctx context.Context) error {
	_, err := c.c.Initialize(ctx, mcplib.InitializeRequest{})
	return err
}

// call invokes a named MCP tool with args and unmarshals the first text content
// of the response into out. Returns an error if the tool reports IsError.
func (c *mcpConn) call(ctx context.Context, tool string, args map[string]any, out any) error {
	req := mcplib.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	res, err := c.c.CallTool(ctx, req)
	if err != nil {
		return fmt.Errorf("remotevault: %s: %w", tool, err)
	}
	if res.IsError {
		return fmt.Errorf("remotevault: %s: remote error: %s", tool, firstText(res))
	}
	if out == nil {
		return nil
	}
	text := firstText(res)
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("remotevault: %s: unmarshal response: %w", tool, err)
	}
	return nil
}

func firstText(res *mcplib.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
