package api

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()

	if cfg.MaxRetries != 3 {
		t.Errorf("expected MaxRetries=3, got %d", cfg.MaxRetries)
	}
	if cfg.InitialBackoff != 1*time.Second {
		t.Errorf("expected InitialBackoff=1s, got %v", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 30*time.Second {
		t.Errorf("expected MaxBackoff=30s, got %v", cfg.MaxBackoff)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("expected Multiplier=2.0, got %f", cfg.Multiplier)
	}
	if cfg.Jitter != 0.1 {
		t.Errorf("expected Jitter=0.1, got %f", cfg.Jitter)
	}
}

func TestWithRetry_Success(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		Jitter:         0,
	}

	callCount := 0
	result, err := WithRetry(context.Background(), cfg, func() (string, error) {
		callCount++
		return "success", nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result != "success" {
		t.Errorf("expected 'success', got '%s'", result)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestWithRetry_SuccessAfterRetry(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		Jitter:         0,
	}

	callCount := 0
	result, err := WithRetry(context.Background(), cfg, func() (string, error) {
		callCount++
		if callCount < 3 {
			// Return a retryable error
			return "", &APIError{
				Type:         "error",
				ErrorDetails: ErrorDetail{Type: ErrorTypeRateLimit, Message: "rate limited"},
			}
		}
		return "success", nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result != "success" {
		t.Errorf("expected 'success', got '%s'", result)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestWithRetry_MaxRetriesExceeded(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		Jitter:         0,
	}

	callCount := 0
	result, err := WithRetry(context.Background(), cfg, func() (string, error) {
		callCount++
		return "", &APIError{
			Type:         "error",
			ErrorDetails: ErrorDetail{Type: ErrorTypeRateLimit, Message: "rate limited"},
		}
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
	if callCount != 3 { // 1 initial + 2 retries
		t.Errorf("expected 3 calls, got %d", callCount)
	}

	// Check that it's wrapped as RetryableError
	var retryErr *RetryableError
	if !errors.As(err, &retryErr) {
		t.Errorf("expected RetryableError, got %T", err)
	} else {
		if !retryErr.Info.Retried {
			t.Error("expected Retried=true")
		}
		if retryErr.Info.TotalAttempts != 3 {
			t.Errorf("expected TotalAttempts=3, got %d", retryErr.Info.TotalAttempts)
		}
	}
}

func TestWithRetry_NonRetryableError(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		Jitter:         0,
	}

	callCount := 0
	result, err := WithRetry(context.Background(), cfg, func() (string, error) {
		callCount++
		return "", &APIError{
			Type:         "error",
			ErrorDetails: ErrorDetail{Type: ErrorTypeAuthentication, Message: "invalid api key"},
		}
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
	if callCount != 1 { // Should not retry
		t.Errorf("expected 1 call, got %d", callCount)
	}

	// Check that it's wrapped as RetryableError with no retries
	var retryErr *RetryableError
	if !errors.As(err, &retryErr) {
		t.Errorf("expected RetryableError, got %T", err)
	} else {
		if retryErr.Info.Retried {
			t.Error("expected Retried=false for non-retryable error")
		}
	}
}

func TestWithRetry_ContextCanceled(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second, // Long backoff
		MaxBackoff:     5 * time.Second,
		Multiplier:     2.0,
		Jitter:         0,
	}

	ctx, cancel := context.WithCancel(context.Background())

	callCount := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := WithRetry(ctx, cfg, func() (string, error) {
		callCount++
		return "", &APIError{
			Type:         "error",
			ErrorDetails: ErrorDetail{Type: ErrorTypeRateLimit, Message: "rate limited"},
		}
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestShouldRetry_APIErrors(t *testing.T) {
	tests := []struct {
		name     string
		errType  ErrorType
		expected bool
	}{
		{"rate limit", ErrorTypeRateLimit, true},
		{"overloaded", ErrorTypeOverloaded, true},
		{"api error", ErrorTypeAPI, true},
		{"authentication", ErrorTypeAuthentication, false},
		{"permission", ErrorTypePermission, false},
		{"invalid request", ErrorTypeInvalidRequest, false},
		{"not found", ErrorTypeNotFound, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &APIError{
				Type:         "error",
				ErrorDetails: ErrorDetail{Type: tt.errType, Message: "test"},
			}
			result := ShouldRetry(err)
			if result != tt.expected {
				t.Errorf("ShouldRetry for %s: expected %v, got %v", tt.name, tt.expected, result)
			}
		})
	}
}

func TestShouldRetry_NetworkErrors(t *testing.T) {
	// Test timeout error
	timeoutErr := &net.DNSError{
		Err:       "i/o timeout",
		IsTimeout: true,
	}
	if !ShouldRetry(timeoutErr) {
		t.Error("expected timeout error to be retryable")
	}

	// Test context errors (not retryable)
	if ShouldRetry(context.Canceled) {
		t.Error("expected context.Canceled to not be retryable")
	}
	if ShouldRetry(context.DeadlineExceeded) {
		t.Error("expected context.DeadlineExceeded to not be retryable")
	}

	// Test nil error
	if ShouldRetry(nil) {
		t.Error("expected nil error to not be retryable")
	}
}

func TestIsRetryableHTTPStatus(t *testing.T) {
	tests := []struct {
		status   int
		expected bool
	}{
		{429, true},  // Too Many Requests
		{529, true},  // Overloaded
		{502, true},  // Bad Gateway
		{503, true},  // Service Unavailable
		{504, true},  // Gateway Timeout
		{400, false}, // Bad Request
		{401, false}, // Unauthorized
		{403, false}, // Forbidden
		{404, false}, // Not Found
		{500, false}, // Internal Server Error
		{200, false}, // OK
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := IsRetryableHTTPStatus(tt.status)
			if result != tt.expected {
				t.Errorf("IsRetryableHTTPStatus(%d): expected %v, got %v", tt.status, tt.expected, result)
			}
		})
	}
}

func TestErrorHelpers(t *testing.T) {
	rateLimitErr := &APIError{
		Type:         "error",
		ErrorDetails: ErrorDetail{Type: ErrorTypeRateLimit, Message: "rate limited"},
	}
	authErr := &APIError{
		Type:         "error",
		ErrorDetails: ErrorDetail{Type: ErrorTypeAuthentication, Message: "unauthorized"},
	}
	permErr := &APIError{
		Type:         "error",
		ErrorDetails: ErrorDetail{Type: ErrorTypePermission, Message: "forbidden"},
	}
	invalidErr := &APIError{
		Type:         "error",
		ErrorDetails: ErrorDetail{Type: ErrorTypeInvalidRequest, Message: "bad request"},
	}
	notFoundErr := &APIError{
		Type:         "error",
		ErrorDetails: ErrorDetail{Type: ErrorTypeNotFound, Message: "not found"},
	}
	overloadedErr := &APIError{
		Type:         "error",
		ErrorDetails: ErrorDetail{Type: ErrorTypeOverloaded, Message: "overloaded"},
	}

	if !IsRateLimitError(rateLimitErr) {
		t.Error("IsRateLimitError should return true for rate limit error")
	}
	if !IsAuthenticationError(authErr) {
		t.Error("IsAuthenticationError should return true for auth error")
	}
	if !IsPermissionError(permErr) {
		t.Error("IsPermissionError should return true for permission error")
	}
	if !IsInvalidRequestError(invalidErr) {
		t.Error("IsInvalidRequestError should return true for invalid request error")
	}
	if !IsNotFoundError(notFoundErr) {
		t.Error("IsNotFoundError should return true for not found error")
	}
	if !IsOverloadedError(overloadedErr) {
		t.Error("IsOverloadedError should return true for overloaded error")
	}
}

func TestRetryableError(t *testing.T) {
	underlying := &APIError{
		Type:         "error",
		ErrorDetails: ErrorDetail{Type: ErrorTypeRateLimit, Message: "rate limited"},
	}

	retryErr := &RetryableError{
		Err: underlying,
		Info: RetryInfo{
			Attempt:       1,
			TotalAttempts: 3,
			Retried:       true,
			LastError:     underlying,
		},
		Reason: "max retries exceeded",
	}

	// Test Error() method
	errStr := retryErr.Error()
	if errStr == "" {
		t.Error("Error() should return non-empty string")
	}

	// Test Unwrap()
	unwrapped := retryErr.Unwrap()
	if unwrapped != underlying {
		t.Error("Unwrap() should return underlying error")
	}

	// Test errors.Is
	if !errors.Is(retryErr, underlying) {
		t.Error("errors.Is should work with underlying error")
	}
}

func TestExtractAPIError(t *testing.T) {
	apiErr := &APIError{
		Type:         "error",
		ErrorDetails: ErrorDetail{Type: ErrorTypeRateLimit, Message: "test"},
	}

	// Direct extraction
	extracted := ExtractAPIError(apiErr)
	if extracted != apiErr {
		t.Error("ExtractAPIError should return the APIError directly")
	}

	// Wrapped extraction
	wrapped := &RetryableError{Err: apiErr}
	extracted = ExtractAPIError(wrapped)
	if extracted != apiErr {
		t.Error("ExtractAPIError should unwrap and find APIError")
	}

	// Nil check
	extracted = ExtractAPIError(nil)
	if extracted != nil {
		t.Error("ExtractAPIError should return nil for nil error")
	}

	// Non-APIError
	extracted = ExtractAPIError(errors.New("generic error"))
	if extracted != nil {
		t.Error("ExtractAPIError should return nil for non-APIError")
	}
}

func TestExtractRetryInfo(t *testing.T) {
	info := RetryInfo{
		Attempt:       2,
		TotalAttempts: 3,
		Retried:       true,
	}

	retryErr := &RetryableError{
		Err:  errors.New("test"),
		Info: info,
	}

	// Extract from RetryableError
	extracted := ExtractRetryInfo(retryErr)
	if extracted == nil {
		t.Error("ExtractRetryInfo should return info from RetryableError")
	}
	if extracted.TotalAttempts != 3 {
		t.Errorf("expected TotalAttempts=3, got %d", extracted.TotalAttempts)
	}

	// Non-RetryableError
	extracted = ExtractRetryInfo(errors.New("generic error"))
	if extracted != nil {
		t.Error("ExtractRetryInfo should return nil for non-RetryableError")
	}
}

func TestWithRetry_OnRetryCallback(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		Jitter:         0,
	}

	callbackInvocations := 0
	cfg.OnRetry = func(attempt int, err error, backoff time.Duration) {
		callbackInvocations++
	}

	callCount := 0
	_, _ = WithRetry(context.Background(), cfg, func() (string, error) {
		callCount++
		if callCount < 3 {
			return "", &APIError{
				Type:         "error",
				ErrorDetails: ErrorDetail{Type: ErrorTypeRateLimit, Message: "rate limited"},
			}
		}
		return "success", nil
	})

	// Should be called before each retry (2 retries total)
	if callbackInvocations != 2 {
		t.Errorf("expected OnRetry to be called 2 times, got %d", callbackInvocations)
	}
}

func TestParseRetryAfterSeconds(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"10", 10},
		{"0", 0},
		{"123", 123},
		{"10abc", 10},
		{"", 0},
		{"abc", 0},
	}

	for _, tt := range tests {
		result := parseRetryAfterSeconds(tt.input)
		if result != tt.expected {
			t.Errorf("parseRetryAfterSeconds(%q): expected %d, got %d", tt.input, tt.expected, result)
		}
	}
}
