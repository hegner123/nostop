package storage

import "errors"

// Common errors for storage operations.
var (
	ErrTopicNotFound        = errors.New("topic not found")
	ErrConversationNotFound = errors.New("conversation not found")
	ErrMessageNotFound      = errors.New("message not found")
)
