package nostop

import (
	"errors"
	"testing"

	"github.com/hegner123/nostop/internal/api"
)

func TestNostopError(t *testing.T) {
	underlying := errors.New("underlying error")
	nostopErr := &NostopError{
		Op:          "Send",
		Err:         underlying,
		Retry:       true,
		Attempt:     3,
		Recoverable: false,
		UserMessage: "An error occurred",
	}

	// Test Error() method
	errStr := nostopErr.Error()
	if errStr == "" {
		t.Error("Error() should return non-empty string")
	}
	if errStr != "Send: underlying error (after 3 attempts)" {
		t.Errorf("unexpected error string: %s", errStr)
	}

	// Test Unwrap()
	unwrapped := nostopErr.Unwrap()
	if unwrapped != underlying {
		t.Error("Unwrap() should return underlying error")
	}

	// Test Is()
	if !errors.Is(nostopErr, underlying) {
		t.Error("errors.Is should work with underlying error")
	}
}

func TestNostopError_NoRetry(t *testing.T) {
	underlying := errors.New("underlying error")
	nostopErr := &NostopError{
		Op:          "Archive",
		Err:         underlying,
		Retry:       false,
		Attempt:     1,
		Recoverable: true,
		UserMessage: "Archive failed",
	}

	errStr := nostopErr.Error()
	if errStr != "Archive: underlying error" {
		t.Errorf("unexpected error string: %s", errStr)
	}
}

func TestNewNostopError(t *testing.T) {
	apiErr := &api.APIError{
		Type:         "error",
		ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeRateLimit, Message: "rate limited"},
	}

	nostopErr := NewNostopError("Send", apiErr)

	if nostopErr.Op != "Send" {
		t.Errorf("expected Op='Send', got '%s'", nostopErr.Op)
	}
	if !errors.Is(nostopErr, apiErr) {
		t.Error("should wrap the API error")
	}
	if !nostopErr.Recoverable {
		t.Error("rate limit error should be recoverable")
	}
}

func TestIsRecoverable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, true},
		{"context full", ErrContextFull, true},
		{"token count failed", ErrTokenCountFailed, true},
		{"topic detection failed", ErrTopicDetectionFailed, true},
		{"scoring failed", ErrScoringFailed, true},
		{"API unavailable", ErrAPIUnavailable, true},
		{"no active conv", ErrNoActiveConv, false},
		{"rate limit API error", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeRateLimit, Message: "rate limited"},
		}, true},
		{"overloaded API error", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeOverloaded, Message: "overloaded"},
		}, true},
		{"auth API error", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeAuthentication, Message: "unauthorized"},
		}, false},
		{"permission API error", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypePermission, Message: "forbidden"},
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRecoverable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRecoverable for %s: expected %v, got %v", tt.name, tt.expected, result)
			}
		})
	}
}

func TestUserFriendlyMessage(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		contains string
	}{
		{"nil error", nil, ""},
		{"context full", ErrContextFull, "too long"},
		{"no active conv", ErrNoActiveConv, "No active conversation"},
		{"storage failure", ErrStorageFailure, "save data"},
		{"API unavailable", ErrAPIUnavailable, "temporarily unavailable"},
		{"token count failed", ErrTokenCountFailed, "estimate"},
		{"topic detection failed", ErrTopicDetectionFailed, "topic changes"},
		{"scoring failed", ErrScoringFailed, "default values"},
		{"rate limit", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeRateLimit, Message: "rate limited"},
		}, "rate limit"},
		{"overloaded", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeOverloaded, Message: "overloaded"},
		}, "busy"},
		{"auth error", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeAuthentication, Message: "unauthorized"},
		}, "API key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := UserFriendlyMessage(tt.err)
			if tt.contains == "" {
				if result != "" {
					t.Errorf("expected empty string, got '%s'", result)
				}
				return
			}
			if !containsIgnoreCase(result, tt.contains) {
				t.Errorf("expected message containing '%s', got '%s'", tt.contains, result)
			}
		})
	}
}

func TestCategorizeError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ErrorCategory
	}{
		{"nil error", nil, ErrorCategoryUnknown},
		{"context full", ErrContextFull, ErrorCategoryContextFull},
		{"storage failure", ErrStorageFailure, ErrorCategoryStorage},
		{"rate limit", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeRateLimit, Message: "rate limited"},
		}, ErrorCategoryRateLimit},
		{"overloaded", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeOverloaded, Message: "overloaded"},
		}, ErrorCategoryOverloaded},
		{"auth error", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeAuthentication, Message: "unauthorized"},
		}, ErrorCategoryAuthentication},
		{"invalid request", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeInvalidRequest, Message: "bad request"},
		}, ErrorCategoryInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CategorizeError(tt.err)
			if result != tt.expected {
				t.Errorf("CategorizeError for %s: expected %v, got %v", tt.name, tt.expected, result)
			}
		})
	}
}

func TestErrorCategoryString(t *testing.T) {
	tests := []struct {
		category ErrorCategory
		expected string
	}{
		{ErrorCategoryUnknown, "unknown"},
		{ErrorCategoryNetwork, "network"},
		{ErrorCategoryAuthentication, "authentication"},
		{ErrorCategoryRateLimit, "rate_limit"},
		{ErrorCategoryOverloaded, "overloaded"},
		{ErrorCategoryInvalidRequest, "invalid_request"},
		{ErrorCategoryStorage, "storage"},
		{ErrorCategoryContextFull, "context_full"},
	}

	for _, tt := range tests {
		result := tt.category.String()
		if result != tt.expected {
			t.Errorf("ErrorCategory.String() for %d: expected '%s', got '%s'", tt.category, tt.expected, result)
		}
	}
}

func TestSuggestAction(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected SuggestedAction
	}{
		{"nil error", nil, ActionNone},
		{"context full", ErrContextFull, ActionArchiveTopics},
		{"no active conv", ErrNoActiveConv, ActionStartNewConversation},
		{"rate limit", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeRateLimit, Message: "rate limited"},
		}, ActionWait},
		{"overloaded", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeOverloaded, Message: "overloaded"},
		}, ActionWait},
		{"auth error", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeAuthentication, Message: "unauthorized"},
		}, ActionCheckAPIKey},
		{"invalid request", &api.APIError{
			Type:         "error",
			ErrorDetails: api.ErrorDetail{Type: api.ErrorTypeInvalidRequest, Message: "bad request"},
		}, ActionReduceInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SuggestAction(tt.err)
			if result != tt.expected {
				t.Errorf("SuggestAction for %s: expected %v, got %v", tt.name, tt.expected, result)
			}
		})
	}
}

func TestSuggestedActionString(t *testing.T) {
	tests := []struct {
		action   SuggestedAction
		expected string
	}{
		{ActionNone, "none"},
		{ActionRetry, "retry"},
		{ActionWait, "wait"},
		{ActionCheckAPIKey, "check_api_key"},
		{ActionReduceInput, "reduce_input"},
		{ActionArchiveTopics, "archive_topics"},
		{ActionStartNewConversation, "start_new_conversation"},
	}

	for _, tt := range tests {
		result := tt.action.String()
		if result != tt.expected {
			t.Errorf("SuggestedAction.String() for %d: expected '%s', got '%s'", tt.action, tt.expected, result)
		}
	}
}

func TestActionMessage(t *testing.T) {
	tests := []struct {
		action   SuggestedAction
		contains string
	}{
		{ActionNone, ""},
		{ActionRetry, "Try again"},
		{ActionWait, "wait"},
		{ActionCheckAPIKey, "API key"},
		{ActionReduceInput, "shorter"},
		{ActionArchiveTopics, "Archiving"},
		{ActionStartNewConversation, "new conversation"},
	}

	for _, tt := range tests {
		result := ActionMessage(tt.action)
		if tt.contains == "" {
			if result != "" {
				t.Errorf("expected empty string for ActionNone, got '%s'", result)
			}
			continue
		}
		if !containsIgnoreCase(result, tt.contains) {
			t.Errorf("ActionMessage for %s: expected message containing '%s', got '%s'", tt.action.String(), tt.contains, result)
		}
	}
}

func TestGetRetryInfo(t *testing.T) {
	// Without retry info
	retried, attempts := GetRetryInfo(errors.New("generic error"))
	if retried || attempts != 1 {
		t.Errorf("expected retried=false, attempts=1; got retried=%v, attempts=%d", retried, attempts)
	}

	// With retry info via RetryableError
	retryErr := &api.RetryableError{
		Err: errors.New("test"),
		Info: api.RetryInfo{
			Retried:       true,
			TotalAttempts: 3,
		},
	}
	retried, attempts = GetRetryInfo(retryErr)
	if !retried || attempts != 3 {
		t.Errorf("expected retried=true, attempts=3; got retried=%v, attempts=%d", retried, attempts)
	}
}

func TestWrapError(t *testing.T) {
	underlying := errors.New("underlying error")
	wrapped := WrapError("Archive", underlying, true, "Archive failed")

	if wrapped.Op != "Archive" {
		t.Errorf("expected Op='Archive', got '%s'", wrapped.Op)
	}
	if !wrapped.Recoverable {
		t.Error("expected Recoverable=true")
	}
	if wrapped.UserMessage != "Archive failed" {
		t.Errorf("expected UserMessage='Archive failed', got '%s'", wrapped.UserMessage)
	}
	if !errors.Is(wrapped, underlying) {
		t.Error("should wrap the underlying error")
	}
}

// Note: containsIgnoreCase is already defined in archiver.go
