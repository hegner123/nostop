package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPTransport communicates with an MCP server over HTTP POST.
// Each Send() is a single HTTP request/response round-trip.
// SSE handling is deferred to Phase 5 (resource subscriptions).
type HTTPTransport struct {
	url     string
	headers map[string]string
	client  *http.Client
}

// NewHTTPTransport creates a new HTTPTransport for the given URL.
func NewHTTPTransport(url string, headers map[string]string, timeout time.Duration) *HTTPTransport {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &HTTPTransport{
		url:     url,
		headers: headers,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Send sends a JSON-RPC request over HTTP POST and returns the response.
func (t *HTTPTransport) Send(ctx context.Context, req *Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &resp, nil
}

// Notify sends a JSON-RPC notification over HTTP POST (no response expected).
func (t *HTTPTransport) Notify(ctx context.Context, notif *Notification) error {
	body, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Notifications don't require a specific response, but we check for errors.
	if httpResp.StatusCode != http.StatusOK && httpResp.StatusCode != http.StatusAccepted && httpResp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	return nil
}

// Close is a no-op for HTTP transport (stateless).
func (t *HTTPTransport) Close() error {
	return nil
}
