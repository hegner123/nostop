package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
)

// ProtocolVersion is the MCP protocol version this client declares.
const ProtocolVersion = "2025-03-26"

// Client wraps a Transport and implements the MCP protocol methods.
type Client struct {
	transport Transport
	info      ServerInfo
	nextID    atomic.Int64 // generates integer IDs; accepts any ID type in responses
}

// NewClient creates a new MCP client using the given transport.
// Call Initialize() to perform the MCP handshake before using other methods.
func NewClient(transport Transport) *Client {
	return &Client{
		transport: transport,
	}
}

// Initialize performs the MCP initialize handshake.
// This must be called before any other MCP method.
//
// Sends: method "initialize" with protocolVersion, capabilities, and clientInfo.
// Response contains the server's protocolVersion, capabilities, and serverInfo.
// After receiving the response, sends a "notifications/initialized" notification.
func (c *Client) Initialize(ctx context.Context, clientName, clientVersion string) (*ServerInfo, error) {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    clientName,
			"version": clientVersion,
		},
	}

	resp, err := c.call(ctx, "initialize", params)
	if err != nil {
		return nil, fmt.Errorf("initialize failed: %w", err)
	}

	// Parse the initialize result.
	resultBytes, marshalErr := json.Marshal(resp.Result)
	if marshalErr != nil {
		return nil, fmt.Errorf("failed to marshal initialize result: %w", marshalErr)
	}

	var initResult InitializeResult
	if unmarshalErr := json.Unmarshal(resultBytes, &initResult); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to parse initialize result: %w", unmarshalErr)
	}

	c.info = initResult.ServerInfo
	c.info.ProtocolVersion = initResult.ProtocolVersion
	c.info.Capabilities = initResult.Capabilities

	// Send initialized notification to complete the handshake.
	notif := NewNotification("notifications/initialized", nil)
	if notifyErr := c.transport.Notify(ctx, notif); notifyErr != nil {
		log.Printf("[mcp] warning: failed to send initialized notification: %v", notifyErr)
	}

	return &c.info, nil
}

// Ping sends a ping request to the server.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.call(ctx, "ping", nil)
	return err
}

// Shutdown sends a shutdown notification to the server and closes the transport.
func (c *Client) Shutdown(ctx context.Context) error {
	// Some servers support a shutdown notification; send it best-effort.
	notif := NewNotification("notifications/cancelled", map[string]any{
		"reason": "client shutdown",
	})
	c.transport.Notify(ctx, notif)
	return c.transport.Close()
}

// ListTools retrieves the list of tools from the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}

	resultBytes, marshalErr := json.Marshal(resp.Result)
	if marshalErr != nil {
		return nil, fmt.Errorf("failed to marshal tools/list result: %w", marshalErr)
	}

	var result ToolListResult
	if unmarshalErr := json.Unmarshal(resultBytes, &result); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to parse tools/list result: %w", unmarshalErr)
	}

	return result.Tools, nil
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	params := map[string]any{
		"name": name,
	}
	if args != nil {
		params["arguments"] = args
	}

	resp, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tools/call %q failed: %w", name, err)
	}

	resultBytes, marshalErr := json.Marshal(resp.Result)
	if marshalErr != nil {
		return nil, fmt.Errorf("failed to marshal tools/call result: %w", marshalErr)
	}

	var result ToolResult
	if unmarshalErr := json.Unmarshal(resultBytes, &result); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to parse tools/call result: %w", unmarshalErr)
	}

	return &result, nil
}

// Info returns the server info from the last Initialize call.
func (c *Client) Info() ServerInfo {
	return c.info
}

// call sends a JSON-RPC request and returns the response, checking for errors.
func (c *Client) call(ctx context.Context, method string, params map[string]any) (*Response, error) {
	id := c.nextID.Add(1)
	req := NewRequest(id, method, params)

	resp, err := c.transport.Send(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, resp.Error
	}

	return resp, nil
}
