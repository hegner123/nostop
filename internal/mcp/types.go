// Package mcp implements the Model Context Protocol (MCP) client.
// MCP is a JSON-RPC 2.0 based protocol for connecting AI clients
// to tool servers that expose tools, resources, and prompts.
package mcp

// ServerCapabilities describes what an MCP server supports.
type ServerCapabilities struct {
	Tools     *struct{} `json:"tools,omitempty"`
	Resources *struct{} `json:"resources,omitempty"`
	Prompts   *struct{} `json:"prompts,omitempty"`
}

// ServerInfo holds information returned by a server's initialize response.
type ServerInfo struct {
	Name            string             `json:"name"`
	Version         string             `json:"version"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ProtocolVersion string             `json:"protocolVersion"`
}

// ToolInfo describes a tool exposed by an MCP server.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolResult holds the result of a tool call.
type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock represents a content block in an MCP tool result.
// Phase 1 only handles type: "text". Non-text blocks (image, resource)
// are logged as warnings. Do not add speculative fields for future types.
type ContentBlock struct {
	Type string `json:"type"` // "text", "image", "resource"
	Text string `json:"text,omitempty"`
}

// ClientInfo identifies the client during the initialize handshake.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeParams holds the parameters for the initialize request.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
}

// InitializeResult holds the result of the initialize response.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
}

// ToolCallParams holds the parameters for a tools/call request.
type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolListResult holds the result of a tools/list response.
type ToolListResult struct {
	Tools []ToolInfo `json:"tools"`
}

// ServerStatus represents the lifecycle state of a managed MCP server.
type ServerStatus int

const (
	ServerStarting ServerStatus = iota
	ServerReady
	ServerError
	ServerStopped
)

// String returns a human-readable status string.
func (s ServerStatus) String() string {
	switch s {
	case ServerStarting:
		return "Starting"
	case ServerReady:
		return "Ready"
	case ServerError:
		return "Error"
	case ServerStopped:
		return "Stopped"
	default:
		return "Unknown"
	}
}
