package mcp

import "context"

// Transport is the interface for JSON-RPC 2.0 communication with an MCP server.
// Two implementations exist: StdioTransport (subprocess) and HTTPTransport (remote).
type Transport interface {
	// Send sends a JSON-RPC request and waits for the response.
	// The context controls the timeout for this individual call.
	Send(ctx context.Context, req *Request) (*Response, error)

	// Notify sends a JSON-RPC notification (no response expected).
	Notify(ctx context.Context, notif *Notification) error

	// Close shuts down the transport, releasing all resources.
	// For StdioTransport this kills the subprocess and waits for goroutines.
	// For HTTPTransport this is a no-op (stateless).
	Close() error
}
