// Package rlm provides the main RLM engine for intelligent topic-based context archival.
package rlm

import (
	"errors"
	"fmt"

	"github.com/user/rlm/internal/api"
)

// Sentinel errors for RLM operations.
var (
	// ErrContextFull indicates the context window is full and cannot accept more content.
	ErrContextFull = errors.New("context window full")

	// ErrNoActiveConv indicates no active conversation is available.
	ErrNoActiveConv = errors.New("no active conversation")

	// ErrStorageFailure indicates a storage operation failed.
	ErrStorageFailure = errors.New("storage operation failed")

	// ErrAPIUnavailable indicates the API is temporarily unavailable.
	ErrAPIUnavailable = errors.New("API temporarily unavailable")

	// ErrTokenCountFailed indicates token counting failed.
	ErrTokenCountFailed = errors.New("token counting failed")

	// ErrTopicDetectionFailed indicates topic detection failed.
	ErrTopicDetectionFailed = errors.New("topic detection failed")

	// ErrScoringFailed indicates relevance scoring failed.
	ErrScoringFailed = errors.New("relevance scoring failed")

	// ErrArchiveFailed indicates archival operation failed.
	ErrArchiveFailed = errors.New("archive operation failed")

	// ErrRestoreFailed indicates topic restoration failed.
	ErrRestoreFailed = errors.New("restore operation failed")
)

// RLMError wraps errors with context about the operation that failed.
type RLMError struct {
	// Op is the operation that failed (e.g., "Send", "Archive", "Restore").
	Op string

	// Err is the underlying error.
	Err error

	// Retry indicates whether this error was encountered after retries.
	Retry bool

	// Attempt indicates which attempt failed (1 = first attempt).
	Attempt int

	// Recoverable indicates whether the operation can be retried or recovered from.
	Recoverable bool

	// UserMessage is a user-friendly message describing the error.
	UserMessage string
}

// Error implements the error interface.
func (e *RLMError) Error() string {
	if e.Retry {
		return fmt.Sprintf("%s: %v (after %d attempts)", e.Op, e.Err, e.Attempt)
	}
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

// Unwrap returns the underlying error.
func (e *RLMError) Unwrap() error {
	return e.Err
}

// Is checks if the error is of a specific type.
func (e *RLMError) Is(target error) bool {
	return errors.Is(e.Err, target)
}

// NewRLMError creates a new RLMError with the given operation and error.
func NewRLMError(op string, err error) *RLMError {
	rlmErr := &RLMError{
		Op:          op,
		Err:         err,
		Attempt:     1,
		Recoverable: IsRecoverable(err),
		UserMessage: UserFriendlyMessage(err),
	}

	// Check if this came from a retry operation
	if retryInfo := api.ExtractRetryInfo(err); retryInfo != nil {
		rlmErr.Retry = retryInfo.Retried
		rlmErr.Attempt = retryInfo.TotalAttempts
	}

	return rlmErr
}

// WrapError wraps an error with RLM context.
func WrapError(op string, err error, recoverable bool, userMsg string) *RLMError {
	return &RLMError{
		Op:          op,
		Err:         err,
		Recoverable: recoverable,
		UserMessage: userMsg,
	}
}

// IsRecoverable determines if an error is recoverable through retry or graceful degradation.
func IsRecoverable(err error) bool {
	if err == nil {
		return true
	}

	// Check for specific sentinel errors
	switch {
	case errors.Is(err, ErrContextFull):
		return true // Can archive topics to make room
	case errors.Is(err, ErrTokenCountFailed):
		return true // Can use local estimation
	case errors.Is(err, ErrTopicDetectionFailed):
		return true // Can continue without topic detection
	case errors.Is(err, ErrScoringFailed):
		return true // Can use default relevance
	case errors.Is(err, ErrAPIUnavailable):
		return true // Temporary, can retry
	}

	// Check for API errors
	if apiErr := api.ExtractAPIError(err); apiErr != nil {
		// Rate limits and overloaded are recoverable
		if apiErr.IsRateLimited() || apiErr.IsOverloaded() {
			return true
		}
		// Authentication and permission errors are not recoverable
		if api.IsAuthenticationError(err) || api.IsPermissionError(err) {
			return false
		}
	}

	// Check if it's retryable according to the retry logic
	return api.ShouldRetry(err)
}

// IsRetryable returns true if the error can be retried immediately.
func IsRetryable(err error) bool {
	return api.ShouldRetry(err)
}

// UserFriendlyMessage returns a user-friendly message for an error.
func UserFriendlyMessage(err error) string {
	if err == nil {
		return ""
	}

	// Check for specific sentinel errors
	switch {
	case errors.Is(err, ErrContextFull):
		return "The conversation is too long. Archiving older topics to make room."
	case errors.Is(err, ErrNoActiveConv):
		return "No active conversation. Please start a new conversation."
	case errors.Is(err, ErrStorageFailure):
		return "Failed to save data. Please try again."
	case errors.Is(err, ErrAPIUnavailable):
		return "The service is temporarily unavailable. Please try again in a moment."
	case errors.Is(err, ErrTokenCountFailed):
		return "Could not count tokens. Using an estimate instead."
	case errors.Is(err, ErrTopicDetectionFailed):
		return "Could not detect topic changes. Continuing with current topic."
	case errors.Is(err, ErrScoringFailed):
		return "Could not score topic relevance. Using default values."
	case errors.Is(err, ErrArchiveFailed):
		return "Could not archive old topics. The conversation may become slow."
	case errors.Is(err, ErrRestoreFailed):
		return "Could not restore the requested topic. It may no longer be available."
	case errors.Is(err, ErrTopicNotFound):
		return "The requested topic could not be found."
	case errors.Is(err, ErrTopicNotArchived):
		return "The topic is not archived and cannot be restored."
	case errors.Is(err, ErrNoTopicsToArchive):
		return "No topics available to archive."
	}

	// Check for API errors
	if apiErr := api.ExtractAPIError(err); apiErr != nil {
		switch apiErr.ErrorDetails.Type {
		case api.ErrorTypeRateLimit:
			return "Request rate limit reached. Please wait a moment before trying again."
		case api.ErrorTypeOverloaded:
			return "The service is currently busy. Please try again in a moment."
		case api.ErrorTypeAuthentication:
			return "Authentication failed. Please check your API key."
		case api.ErrorTypePermission:
			return "You don't have permission for this operation."
		case api.ErrorTypeInvalidRequest:
			return "The request was invalid. Please check your input."
		case api.ErrorTypeNotFound:
			return "The requested resource was not found."
		default:
			return "An unexpected error occurred. Please try again."
		}
	}

	// Check for retry exhaustion
	if retryInfo := api.ExtractRetryInfo(err); retryInfo != nil && retryInfo.Retried {
		return fmt.Sprintf("Operation failed after %d attempts. Please try again later.", retryInfo.TotalAttempts)
	}

	// Default message
	return "An error occurred. Please try again."
}

// GetRetryInfo extracts retry information from an error.
func GetRetryInfo(err error) (retried bool, attempts int) {
	if retryInfo := api.ExtractRetryInfo(err); retryInfo != nil {
		return retryInfo.Retried, retryInfo.TotalAttempts
	}
	return false, 1
}

// ErrorCategory represents the category of an error for UI display.
type ErrorCategory int

const (
	// ErrorCategoryUnknown indicates an unknown error category.
	ErrorCategoryUnknown ErrorCategory = iota
	// ErrorCategoryNetwork indicates a network-related error.
	ErrorCategoryNetwork
	// ErrorCategoryAuthentication indicates an authentication error.
	ErrorCategoryAuthentication
	// ErrorCategoryRateLimit indicates a rate limit error.
	ErrorCategoryRateLimit
	// ErrorCategoryOverloaded indicates a service overloaded error.
	ErrorCategoryOverloaded
	// ErrorCategoryInvalidRequest indicates an invalid request error.
	ErrorCategoryInvalidRequest
	// ErrorCategoryStorage indicates a storage error.
	ErrorCategoryStorage
	// ErrorCategoryContextFull indicates a context full error.
	ErrorCategoryContextFull
)

// String returns a string representation of the error category.
func (c ErrorCategory) String() string {
	switch c {
	case ErrorCategoryNetwork:
		return "network"
	case ErrorCategoryAuthentication:
		return "authentication"
	case ErrorCategoryRateLimit:
		return "rate_limit"
	case ErrorCategoryOverloaded:
		return "overloaded"
	case ErrorCategoryInvalidRequest:
		return "invalid_request"
	case ErrorCategoryStorage:
		return "storage"
	case ErrorCategoryContextFull:
		return "context_full"
	default:
		return "unknown"
	}
}

// CategorizeError determines the category of an error for UI handling.
func CategorizeError(err error) ErrorCategory {
	if err == nil {
		return ErrorCategoryUnknown
	}

	// Check sentinel errors
	switch {
	case errors.Is(err, ErrContextFull):
		return ErrorCategoryContextFull
	case errors.Is(err, ErrStorageFailure):
		return ErrorCategoryStorage
	}

	// Check API errors
	if apiErr := api.ExtractAPIError(err); apiErr != nil {
		switch apiErr.ErrorDetails.Type {
		case api.ErrorTypeRateLimit:
			return ErrorCategoryRateLimit
		case api.ErrorTypeOverloaded:
			return ErrorCategoryOverloaded
		case api.ErrorTypeAuthentication:
			return ErrorCategoryAuthentication
		case api.ErrorTypePermission:
			return ErrorCategoryAuthentication
		case api.ErrorTypeInvalidRequest:
			return ErrorCategoryInvalidRequest
		}
	}

	// Check for network errors
	if api.ShouldRetry(err) {
		return ErrorCategoryNetwork
	}

	return ErrorCategoryUnknown
}

// SuggestedAction represents a suggested action for error recovery.
type SuggestedAction int

const (
	// ActionNone indicates no action is suggested.
	ActionNone SuggestedAction = iota
	// ActionRetry indicates the user should retry the operation.
	ActionRetry
	// ActionWait indicates the user should wait before retrying.
	ActionWait
	// ActionCheckAPIKey indicates the user should check their API key.
	ActionCheckAPIKey
	// ActionReduceInput indicates the user should reduce their input size.
	ActionReduceInput
	// ActionArchiveTopics indicates topics should be archived.
	ActionArchiveTopics
	// ActionStartNewConversation indicates the user should start a new conversation.
	ActionStartNewConversation
)

// String returns a string representation of the suggested action.
func (a SuggestedAction) String() string {
	switch a {
	case ActionRetry:
		return "retry"
	case ActionWait:
		return "wait"
	case ActionCheckAPIKey:
		return "check_api_key"
	case ActionReduceInput:
		return "reduce_input"
	case ActionArchiveTopics:
		return "archive_topics"
	case ActionStartNewConversation:
		return "start_new_conversation"
	default:
		return "none"
	}
}

// SuggestAction suggests an action for error recovery.
func SuggestAction(err error) SuggestedAction {
	if err == nil {
		return ActionNone
	}

	// Check sentinel errors
	switch {
	case errors.Is(err, ErrContextFull):
		return ActionArchiveTopics
	case errors.Is(err, ErrNoActiveConv):
		return ActionStartNewConversation
	}

	// Check API errors
	if apiErr := api.ExtractAPIError(err); apiErr != nil {
		switch apiErr.ErrorDetails.Type {
		case api.ErrorTypeRateLimit:
			return ActionWait
		case api.ErrorTypeOverloaded:
			return ActionWait
		case api.ErrorTypeAuthentication, api.ErrorTypePermission:
			return ActionCheckAPIKey
		case api.ErrorTypeInvalidRequest:
			return ActionReduceInput
		}
	}

	// For recoverable errors, suggest retry
	if IsRecoverable(err) {
		return ActionRetry
	}

	return ActionNone
}

// ActionMessage returns a user-friendly message describing the suggested action.
func ActionMessage(action SuggestedAction) string {
	switch action {
	case ActionRetry:
		return "Try again"
	case ActionWait:
		return "Please wait a moment and try again"
	case ActionCheckAPIKey:
		return "Please check your API key configuration"
	case ActionReduceInput:
		return "Try sending a shorter message"
	case ActionArchiveTopics:
		return "Archiving older topics to make room..."
	case ActionStartNewConversation:
		return "Start a new conversation"
	default:
		return ""
	}
}
