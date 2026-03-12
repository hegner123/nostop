// Package api provides a client for the Claude Messages API.
package api

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"
)

// RetryConfig configures retry behavior for API requests.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (default: 3).
	MaxRetries int

	// InitialBackoff is the initial wait time before the first retry (default: 1s).
	InitialBackoff time.Duration

	// MaxBackoff is the maximum backoff duration (default: 30s).
	MaxBackoff time.Duration

	// Multiplier is the backoff multiplier for exponential backoff (default: 2.0).
	Multiplier float64

	// Jitter is the random jitter factor (0.1 = 10%) to prevent thundering herd (default: 0.1).
	Jitter float64

	// Debug enables logging of retry attempts.
	Debug bool

	// OnRetry is an optional callback invoked before each retry attempt.
	// It receives the attempt number (1-indexed), the error that caused the retry,
	// and the backoff duration.
	OnRetry func(attempt int, err error, backoff time.Duration)
}

// DefaultRetryConfig returns a RetryConfig with sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
		Jitter:         0.1,
		Debug:          false,
	}
}

// RetryInfo contains information about a retry operation.
type RetryInfo struct {
	// Attempt is the attempt number (1 = first attempt, 2 = first retry, etc.).
	Attempt int

	// TotalAttempts is the total number of attempts made.
	TotalAttempts int

	// Retried indicates whether any retries were made.
	Retried bool

	// LastError is the error from the last failed attempt.
	LastError error
}

// RetryableError wraps an error with retry information.
type RetryableError struct {
	Err    error
	Info   RetryInfo
	Reason string
}

// Error implements the error interface.
func (e *RetryableError) Error() string {
	if e.Info.Retried {
		return e.Err.Error() + " (after " + strconv.Itoa(e.Info.TotalAttempts) + " attempts)"
	}
	return e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *RetryableError) Unwrap() error {
	return e.Err
}

// WithRetry executes a function with retry logic based on the provided config.
// It returns the result and an error (if all attempts fail).
func WithRetry[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error

	backoff := cfg.InitialBackoff
	info := RetryInfo{Attempt: 1}

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		info.Attempt = attempt + 1

		result, err := fn()
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Check if we should retry
		if !ShouldRetry(err) {
			info.TotalAttempts = attempt + 1
			return zero, &RetryableError{
				Err:    err,
				Info:   info,
				Reason: "non-retryable error",
			}
		}

		// Don't retry if we've exhausted attempts
		if attempt >= cfg.MaxRetries {
			break
		}

		// Calculate backoff duration
		waitDuration := calculateBackoff(cfg, backoff, err)

		// Call optional retry callback
		if cfg.OnRetry != nil {
			cfg.OnRetry(attempt+1, err, waitDuration)
		}

		// Wait before retrying
		select {
		case <-ctx.Done():
			info.TotalAttempts = attempt + 1
			info.Retried = attempt > 0
			return zero, &RetryableError{
				Err:    ctx.Err(),
				Info:   info,
				Reason: "context cancelled during backoff",
			}
		case <-time.After(waitDuration):
		}

		// Update backoff for next iteration (exponential)
		backoff = time.Duration(float64(backoff) * cfg.Multiplier)
		if backoff > cfg.MaxBackoff {
			backoff = cfg.MaxBackoff
		}
	}

	info.TotalAttempts = cfg.MaxRetries + 1
	info.Retried = cfg.MaxRetries > 0
	info.LastError = lastErr

	return zero, &RetryableError{
		Err:    lastErr,
		Info:   info,
		Reason: "max retries exceeded",
	}
}

// calculateBackoff calculates the backoff duration for a retry.
// It respects Retry-After headers from rate limit errors.
func calculateBackoff(cfg RetryConfig, currentBackoff time.Duration, err error) time.Duration {
	// Check for rate limit error with Retry-After
	if apiErr := ExtractAPIError(err); apiErr != nil {
		if apiErr.RetryAfter > 0 {
			return apiErr.RetryAfter
		}
	}

	// Apply jitter: random value between [1-jitter, 1+jitter] * backoff
	if cfg.Jitter > 0 {
		jitterFactor := 1.0 + (rand.Float64()*2-1)*cfg.Jitter
		return time.Duration(float64(currentBackoff) * jitterFactor)
	}

	return currentBackoff
}

// ShouldRetry determines if an error is retryable.
// Retryable errors include:
//   - Rate limit errors (429)
//   - Overloaded errors (529)
//   - Temporary network errors
//   - Timeout errors
//
// Non-retryable errors include:
//   - Authentication errors (401)
//   - Invalid request errors (400)
//   - Permission errors (403)
//   - Not found errors (404)
func ShouldRetry(err error) bool {
	if err == nil {
		return false
	}

	// Check for context errors (not retryable)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check for API errors
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return isRetryableAPIError(apiErr)
	}

	// Check for network errors
	if isRetryableNetworkError(err) {
		return true
	}

	return false
}

// isRetryableAPIError checks if an API error is retryable.
func isRetryableAPIError(err *APIError) bool {
	switch err.ErrorDetails.Type {
	case ErrorTypeRateLimit:
		// Rate limit errors are retryable
		return true
	case ErrorTypeOverloaded:
		// Overloaded errors are retryable
		return true
	case ErrorTypeAPI:
		// Generic API errors might be transient
		return true
	case ErrorTypeInvalidRequest, ErrorTypeAuthentication, ErrorTypePermission, ErrorTypeNotFound:
		// These are not retryable
		return false
	default:
		// Unknown error types - be conservative and don't retry
		return false
	}
}

// isRetryableNetworkError checks if an error is a retryable network error.
func isRetryableNetworkError(err error) bool {
	// Check for timeout
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Check for specific retryable error types rather than brittle string matching
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ENETUNREACH) {
		return true
	}

	// Check for DNS resolution failures
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.Temporary()
	}

	return false
}

// IsRetryableHTTPStatus returns true if the HTTP status code is retryable.
func IsRetryableHTTPStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests: // 429
		return true
	case 529: // Overloaded (non-standard)
		return true
	case http.StatusBadGateway: // 502
		return true
	case http.StatusServiceUnavailable: // 503
		return true
	case http.StatusGatewayTimeout: // 504
		return true
	default:
		return false
	}
}

// ExtractAPIError extracts an APIError from an error chain.
func ExtractAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return nil
}

// ExtractRetryInfo extracts RetryInfo from an error if available.
func ExtractRetryInfo(err error) *RetryInfo {
	var retryErr *RetryableError
	if errors.As(err, &retryErr) {
		return &retryErr.Info
	}
	return nil
}

// IsRateLimitError returns true if the error is a rate limit error.
func IsRateLimitError(err error) bool {
	apiErr := ExtractAPIError(err)
	return apiErr != nil && apiErr.IsRateLimited()
}

// IsOverloadedError returns true if the error is an overloaded error.
func IsOverloadedError(err error) bool {
	apiErr := ExtractAPIError(err)
	return apiErr != nil && apiErr.IsOverloaded()
}

// IsAuthenticationError returns true if the error is an authentication error.
func IsAuthenticationError(err error) bool {
	apiErr := ExtractAPIError(err)
	return apiErr != nil && apiErr.ErrorDetails.Type == ErrorTypeAuthentication
}

// IsPermissionError returns true if the error is a permission error.
func IsPermissionError(err error) bool {
	apiErr := ExtractAPIError(err)
	return apiErr != nil && apiErr.ErrorDetails.Type == ErrorTypePermission
}

// IsInvalidRequestError returns true if the error is an invalid request error.
func IsInvalidRequestError(err error) bool {
	apiErr := ExtractAPIError(err)
	return apiErr != nil && apiErr.ErrorDetails.Type == ErrorTypeInvalidRequest
}

// IsNotFoundError returns true if the error is a not found error.
func IsNotFoundError(err error) bool {
	apiErr := ExtractAPIError(err)
	return apiErr != nil && apiErr.ErrorDetails.Type == ErrorTypeNotFound
}
