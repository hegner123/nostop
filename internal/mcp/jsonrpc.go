package mcp

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC 2.0 protocol version.
const jsonrpcVersion = "2.0"

// Request is a JSON-RPC 2.0 request message.
// ID is `any` because the spec allows string, number, or null.
type Request struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response message.
type Response struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *RPCError      `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	if e.Data != nil {
		return fmt.Sprintf("JSON-RPC error %d: %s (data: %v)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// Notification is a JSON-RPC 2.0 notification (no ID, no response expected).
type Notification struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// NewRequest creates a JSON-RPC 2.0 request with the given ID, method, and params.
func NewRequest(id any, method string, params map[string]any) *Request {
	return &Request{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Method:  method,
		Params:  params,
	}
}

// NewNotification creates a JSON-RPC 2.0 notification (no ID).
func NewNotification(method string, params map[string]any) *Notification {
	return &Notification{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  params,
	}
}

// MarshalRequest serializes a request to a JSON byte slice with a trailing newline.
func MarshalRequest(req *Request) ([]byte, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	return append(data, '\n'), nil
}

// MarshalNotification serializes a notification to a JSON byte slice with a trailing newline.
func MarshalNotification(notif *Notification) ([]byte, error) {
	data, err := json.Marshal(notif)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal notification: %w", err)
	}
	return append(data, '\n'), nil
}

// UnmarshalResponse deserializes a JSON byte slice into a Response.
func UnmarshalResponse(data []byte) (*Response, error) {
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// Standard JSON-RPC error codes.
const (
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
)
