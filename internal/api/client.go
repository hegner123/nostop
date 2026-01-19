// Package api provides a client for the Claude Messages API.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultBaseURL is the default base URL for the Claude API.
	DefaultBaseURL = "https://api.anthropic.com"

	// DefaultTimeout is the default HTTP client timeout.
	DefaultTimeout = 5 * time.Minute

	// AnthropicVersion is the API version header value.
	AnthropicVersion = "2023-06-01"
)

// Client is a client for the Claude Messages API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithBaseURL sets a custom base URL for the API.
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient.Timeout = timeout
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// NewClient creates a new Claude API client.
func NewClient(apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: DefaultBaseURL,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Send sends a message request to the Claude API and returns the response.
// This is the non-streaming version of the Messages API.
func (c *Client) Send(ctx context.Context, req *Request) (*Response, error) {
	// Ensure stream is disabled for non-streaming requests
	if req.Stream != nil && *req.Stream {
		return nil, fmt.Errorf("use SendStream for streaming requests")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(httpReq)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, c.parseError(httpResp.StatusCode, respBody)
	}

	var resp Response
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// CountTokens counts the tokens in a message request without sending it.
func (c *Client) CountTokens(ctx context.Context, req *TokenCountRequest) (*TokenCountResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages/count_tokens", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(httpReq)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, c.parseError(httpResp.StatusCode, respBody)
	}

	var resp TokenCountResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// setHeaders sets the required headers for Claude API requests.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", AnthropicVersion)
}

// newRequest creates a new HTTP request with the appropriate headers.
// This is used by both Send and Stream methods.
func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(req)
	return req, nil
}

// parseErrorResponse parses an error response from the API.
func (c *Client) parseErrorResponse(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read error response: %w", err)
	}
	return c.parseError(resp.StatusCode, body)
}

// parseError parses an API error response.
func (c *Client) parseError(statusCode int, body []byte) error {
	var apiErr APIError
	if err := json.Unmarshal(body, &apiErr); err != nil {
		// If we can't parse the error, return a generic error with the status code
		return &APIError{
			Type: "error",
			ErrorDetails: ErrorDetail{
				Type:    ErrorTypeAPI,
				Message: fmt.Sprintf("HTTP %d: %s", statusCode, string(body)),
			},
		}
	}

	// Map HTTP status codes to error types if not already set
	if apiErr.ErrorDetails.Type == "" {
		switch statusCode {
		case http.StatusBadRequest:
			apiErr.ErrorDetails.Type = ErrorTypeInvalidRequest
		case http.StatusUnauthorized:
			apiErr.ErrorDetails.Type = ErrorTypeAuthentication
		case http.StatusForbidden:
			apiErr.ErrorDetails.Type = ErrorTypePermission
		case http.StatusNotFound:
			apiErr.ErrorDetails.Type = ErrorTypeNotFound
		case http.StatusTooManyRequests:
			apiErr.ErrorDetails.Type = ErrorTypeRateLimit
		case http.StatusServiceUnavailable:
			apiErr.ErrorDetails.Type = ErrorTypeOverloaded
		default:
			apiErr.ErrorDetails.Type = ErrorTypeAPI
		}
	}

	return &apiErr
}

// APIKey returns the API key used by this client.
func (c *Client) APIKey() string {
	return c.apiKey
}

// BaseURL returns the base URL used by this client.
func (c *Client) BaseURL() string {
	return c.baseURL
}
