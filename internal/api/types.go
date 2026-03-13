// Package api provides types and client for the Claude Messages API.
package api

import (
	"encoding/json"
	"time"
)

// Role represents the role of a message in a conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	StopReasonEndTurn      StopReason = "end_turn"
	StopReasonMaxTokens    StopReason = "max_tokens"
	StopReasonStopSequence StopReason = "stop_sequence"
	StopReasonToolUse      StopReason = "tool_use"
	StopReasonPauseTurn    StopReason = "pause_turn"
	StopReasonRefusal      StopReason = "refusal"
)

// ContentBlockType identifies the type of a content block.
type ContentBlockType string

const (
	ContentBlockTypeText            ContentBlockType = "text"
	ContentBlockTypeImage           ContentBlockType = "image"
	ContentBlockTypeToolUse         ContentBlockType = "tool_use"
	ContentBlockTypeToolResult      ContentBlockType = "tool_result"
	ContentBlockTypeDocument        ContentBlockType = "document"
	ContentBlockTypeThinking        ContentBlockType = "thinking"
	ContentBlockTypeRedactedThink   ContentBlockType = "redacted_thinking"
	ContentBlockTypeServerToolUse   ContentBlockType = "server_tool_use"
	ContentBlockTypeWebSearchResult ContentBlockType = "web_search_tool_result"
)

// ImageMediaType represents supported image formats.
type ImageMediaType string

const (
	ImageMediaTypeJPEG ImageMediaType = "image/jpeg"
	ImageMediaTypePNG  ImageMediaType = "image/png"
	ImageMediaTypeGIF  ImageMediaType = "image/gif"
	ImageMediaTypeWebP ImageMediaType = "image/webp"
)

// CacheTTL represents the time-to-live for cache control.
type CacheTTL string

const (
	CacheTTL5Min  CacheTTL = "5m"
	CacheTTL1Hour CacheTTL = "1h"
)

// CacheControl specifies cache control settings for a content block.
type CacheControl struct {
	Type string    `json:"type"` // Always "ephemeral"
	TTL  *CacheTTL `json:"ttl,omitempty"`
}

// NewCacheControl creates a new cache control with the given TTL.
// If ttl is nil, defaults to 5 minutes.
func NewCacheControl(ttl *CacheTTL) *CacheControl {
	return &CacheControl{
		Type: "ephemeral",
		TTL:  ttl,
	}
}

// WithCache creates a CacheControl with the specified TTL.
// This is a convenience helper for setting cache control on content blocks.
func WithCache(ttl CacheTTL) *CacheControl {
	return &CacheControl{
		Type: "ephemeral",
		TTL:  &ttl,
	}
}

// WithEphemeralCache creates a CacheControl with default 5-minute TTL.
// Use this for frequently accessed but moderately changing content.
func WithEphemeralCache() *CacheControl {
	ttl := CacheTTL5Min
	return &CacheControl{
		Type: "ephemeral",
		TTL:  &ttl,
	}
}

// WithLongCache creates a CacheControl with 1-hour TTL.
// Use this for stable content like system prompts.
func WithLongCache() *CacheControl {
	ttl := CacheTTL1Hour
	return &CacheControl{
		Type: "ephemeral",
		TTL:  &ttl,
	}
}

// ----------------------------------------------------------------------------
// Input Content Blocks (for requests)
// ----------------------------------------------------------------------------

// TextBlockParam represents a text content block in a request.
type TextBlockParam struct {
	Type         ContentBlockType `json:"type"` // Always "text"
	Text         string           `json:"text"`
	CacheControl *CacheControl    `json:"cache_control,omitempty"`
}

// NewTextBlock creates a new text content block.
func NewTextBlock(text string) TextBlockParam {
	return TextBlockParam{
		Type: ContentBlockTypeText,
		Text: text,
	}
}

// NewTextBlockWithCache creates a new text content block with cache control.
func NewTextBlockWithCache(text string, cache *CacheControl) TextBlockParam {
	return TextBlockParam{
		Type:         ContentBlockTypeText,
		Text:         text,
		CacheControl: cache,
	}
}

// SetCacheControl returns a copy of the TextBlockParam with cache control set.
func (t TextBlockParam) SetCacheControl(cache *CacheControl) TextBlockParam {
	t.CacheControl = cache
	return t
}

// Base64ImageSource represents a base64-encoded image.
type Base64ImageSource struct {
	Type      string         `json:"type"` // Always "base64"
	MediaType ImageMediaType `json:"media_type"`
	Data      string         `json:"data"`
}

// URLImageSource represents an image from a URL.
type URLImageSource struct {
	Type string `json:"type"` // Always "url"
	URL  string `json:"url"`
}

// ImageSource can be either Base64ImageSource or URLImageSource.
// Use the appropriate concrete type when constructing.
type ImageSource struct {
	Type      string         `json:"type"`
	MediaType ImageMediaType `json:"media_type,omitempty"`
	Data      string         `json:"data,omitempty"`
	URL       string         `json:"url,omitempty"`
}

// ImageBlockParam represents an image content block in a request.
type ImageBlockParam struct {
	Type         ContentBlockType `json:"type"` // Always "image"
	Source       ImageSource      `json:"source"`
	CacheControl *CacheControl    `json:"cache_control,omitempty"`
}

// NewBase64ImageBlock creates an image block from base64-encoded data.
func NewBase64ImageBlock(mediaType ImageMediaType, data string) ImageBlockParam {
	return ImageBlockParam{
		Type: ContentBlockTypeImage,
		Source: ImageSource{
			Type:      "base64",
			MediaType: mediaType,
			Data:      data,
		},
	}
}

// NewURLImageBlock creates an image block from a URL.
func NewURLImageBlock(url string) ImageBlockParam {
	return ImageBlockParam{
		Type: ContentBlockTypeImage,
		Source: ImageSource{
			Type: "url",
			URL:  url,
		},
	}
}

// ToolUseBlockParam represents a tool use block in a request (from assistant).
type ToolUseBlockParam struct {
	Type         ContentBlockType `json:"type"` // Always "tool_use"
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Input        map[string]any   `json:"input"`
	CacheControl *CacheControl    `json:"cache_control,omitempty"`
}

// ToolResultBlockParam represents a tool result block in a request (from user).
type ToolResultBlockParam struct {
	Type         ContentBlockType `json:"type"` // Always "tool_result"
	ToolUseID    string           `json:"tool_use_id"`
	Content      any              `json:"content,omitempty"` // string or []ContentBlockParam
	IsError      *bool            `json:"is_error,omitempty"`
	CacheControl *CacheControl    `json:"cache_control,omitempty"`
}

// NewToolResultBlock creates a tool result block with a string response.
func NewToolResultBlock(toolUseID, content string) ToolResultBlockParam {
	return ToolResultBlockParam{
		Type:      ContentBlockTypeToolResult,
		ToolUseID: toolUseID,
		Content:   content,
	}
}

// NewToolResultBlockError creates a tool result block indicating an error.
func NewToolResultBlockError(toolUseID, errorMsg string) ToolResultBlockParam {
	isError := true
	return ToolResultBlockParam{
		Type:      ContentBlockTypeToolResult,
		ToolUseID: toolUseID,
		Content:   errorMsg,
		IsError:   &isError,
	}
}

// ContentBlockParam is a union type for all input content blocks.
// When marshaling, use the concrete types (TextBlockParam, ImageBlockParam, etc.)
type ContentBlockParam struct {
	Type ContentBlockType `json:"type"`

	// Text block fields
	Text string `json:"text,omitempty"`

	// Image block fields
	Source *ImageSource `json:"source,omitempty"`

	// Tool use fields
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// Tool result fields
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   *bool  `json:"is_error,omitempty"`

	// Common
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ----------------------------------------------------------------------------
// Output Content Blocks (from responses)
// ----------------------------------------------------------------------------

// TextBlock represents a text content block in a response.
type TextBlock struct {
	Type string `json:"type"` // Always "text"
	Text string `json:"text"`
}

// ToolUseBlock represents a tool use block in a response.
type ToolUseBlock struct {
	Type  string         `json:"type"` // Always "tool_use"
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ThinkingBlock represents a thinking block in a response (extended thinking).
type ThinkingBlock struct {
	Type      string `json:"type"` // Always "thinking"
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

// RedactedThinkingBlock represents redacted thinking content.
type RedactedThinkingBlock struct {
	Type string `json:"type"` // Always "redacted_thinking"
	Data string `json:"data"`
}

// ContentBlock represents any content block in a response.
// Use GetText(), GetToolUse(), etc. to access typed content.
type ContentBlock struct {
	Type string `json:"type"`

	// Text block
	Text string `json:"text,omitempty"`

	// Tool use block
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// Thinking block
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// Redacted thinking
	Data string `json:"data,omitempty"`
}

// IsText returns true if this is a text block.
func (cb *ContentBlock) IsText() bool {
	return cb.Type == string(ContentBlockTypeText)
}

// IsToolUse returns true if this is a tool use block.
func (cb *ContentBlock) IsToolUse() bool {
	return cb.Type == string(ContentBlockTypeToolUse)
}

// IsThinking returns true if this is a thinking block.
func (cb *ContentBlock) IsThinking() bool {
	return cb.Type == string(ContentBlockTypeThinking)
}

// GetToolInput unmarshals the tool input into the provided target.
func (cb *ContentBlock) GetToolInput(target any) error {
	if len(cb.Input) == 0 {
		return nil
	}
	return json.Unmarshal(cb.Input, target)
}

// ----------------------------------------------------------------------------
// Messages
// ----------------------------------------------------------------------------

// MessageParam represents an input message in a request.
type MessageParam struct {
	Role    Role `json:"role"`
	Content any  `json:"content"` // string or []ContentBlockParam
}

// NewUserMessage creates a user message with text content.
func NewUserMessage(text string) MessageParam {
	return MessageParam{
		Role:    RoleUser,
		Content: text,
	}
}

// NewUserMessageBlocks creates a user message with content blocks.
func NewUserMessageBlocks(blocks ...any) MessageParam {
	return MessageParam{
		Role:    RoleUser,
		Content: blocks,
	}
}

// NewAssistantMessage creates an assistant message with text content.
func NewAssistantMessage(text string) MessageParam {
	return MessageParam{
		Role:    RoleAssistant,
		Content: text,
	}
}

// NewAssistantMessageBlocks creates an assistant message with content blocks.
func NewAssistantMessageBlocks(blocks ...any) MessageParam {
	return MessageParam{
		Role:    RoleAssistant,
		Content: blocks,
	}
}

// ----------------------------------------------------------------------------
// System Prompt
// ----------------------------------------------------------------------------

// SystemParam represents a system prompt, which can be a string or content blocks.
type SystemParam struct {
	value any // string or []TextBlockParam
}

// NewSystemString creates a system prompt from a string.
func NewSystemString(text string) SystemParam {
	return SystemParam{value: text}
}

// NewSystemBlocks creates a system prompt from text blocks (for cache control).
func NewSystemBlocks(blocks ...TextBlockParam) SystemParam {
	return SystemParam{value: blocks}
}

// MarshalJSON implements json.Marshaler.
func (s SystemParam) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.value)
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *SystemParam) UnmarshalJSON(data []byte) error {
	// Try string first
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.value = str
		return nil
	}
	// Try array of text blocks
	var blocks []TextBlockParam
	if err := json.Unmarshal(data, &blocks); err == nil {
		s.value = blocks
		return nil
	}
	return nil
}

// ----------------------------------------------------------------------------
// Tool Definitions
// ----------------------------------------------------------------------------

// Tool defines a tool that Claude can use.
type Tool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl *CacheControl  `json:"cache_control,omitempty"`
}

// ----------------------------------------------------------------------------
// Request
// ----------------------------------------------------------------------------

// Request represents a request to the Messages API.
type Request struct {
	Model         string           `json:"model"`
	Messages      []MessageParam   `json:"messages"`
	MaxTokens     int              `json:"max_tokens"`
	System        *SystemParam     `json:"system,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Stream        *bool            `json:"stream,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	TopK          *int             `json:"top_k,omitempty"`
	Tools         []Tool           `json:"tools,omitempty"`
	ToolChoice    *ToolChoice      `json:"tool_choice,omitempty"`
	Metadata      *RequestMetadata `json:"metadata,omitempty"`
}

// ToolChoice specifies how Claude should use tools.
type ToolChoice struct {
	Type string `json:"type"`           // "auto", "any", "tool", "none"
	Name string `json:"name,omitempty"` // Required when type is "tool"
}

// Tool choice constants
var (
	ToolChoiceAuto = &ToolChoice{Type: "auto"}
	ToolChoiceAny  = &ToolChoice{Type: "any"}
	ToolChoiceNone = &ToolChoice{Type: "none"}
)

// ToolChoiceTool creates a tool choice that forces use of a specific tool.
func ToolChoiceTool(name string) *ToolChoice {
	return &ToolChoice{Type: "tool", Name: name}
}

// RequestMetadata contains optional request metadata.
type RequestMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

// ----------------------------------------------------------------------------
// Response
// ----------------------------------------------------------------------------

// Response represents a response from the Messages API.
type Response struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"` // Always "message"
	Role         Role           `json:"role"` // Always "assistant"
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   StopReason     `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence,omitempty"`
	Usage        Usage          `json:"usage"`
}

// GetText returns all text content concatenated.
func (r *Response) GetText() string {
	var result string
	for _, block := range r.Content {
		if block.IsText() {
			result += block.Text
		}
	}
	return result
}

// GetToolUses returns all tool use blocks.
func (r *Response) GetToolUses() []ContentBlock {
	var tools []ContentBlock
	for _, block := range r.Content {
		if block.IsToolUse() {
			tools = append(tools, block)
		}
	}
	return tools
}

// HasToolUse returns true if the response contains tool use blocks.
func (r *Response) HasToolUse() bool {
	return r.StopReason == StopReasonToolUse
}

// ----------------------------------------------------------------------------
// Usage
// ----------------------------------------------------------------------------

// Usage contains token usage information.
type Usage struct {
	InputTokens              int            `json:"input_tokens"`
	OutputTokens             int            `json:"output_tokens"`
	CacheCreationInputTokens *int           `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     *int           `json:"cache_read_input_tokens,omitempty"`
	CacheCreation            *CacheCreation `json:"cache_creation,omitempty"`
	ServiceTier              *string        `json:"service_tier,omitempty"`
}

// TotalInputTokens returns the total input tokens including cache tokens.
func (u *Usage) TotalInputTokens() int {
	total := u.InputTokens
	if u.CacheCreationInputTokens != nil {
		total += *u.CacheCreationInputTokens
	}
	if u.CacheReadInputTokens != nil {
		total += *u.CacheReadInputTokens
	}
	return total
}

// CacheCreation provides detailed cache creation breakdown.
type CacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}

// ----------------------------------------------------------------------------
// Token Counting
// ----------------------------------------------------------------------------

// TokenCountRequest represents a request to count tokens.
type TokenCountRequest struct {
	Model    string         `json:"model"`
	Messages []MessageParam `json:"messages"`
	System   *SystemParam   `json:"system,omitempty"`
	Tools    []Tool         `json:"tools,omitempty"`
}

// TokenCountResponse represents the response from token counting.
type TokenCountResponse struct {
	InputTokens int `json:"input_tokens"`
}

// ----------------------------------------------------------------------------
// Errors
// ----------------------------------------------------------------------------

// ErrorType represents the type of API error.
type ErrorType string

const (
	ErrorTypeInvalidRequest ErrorType = "invalid_request_error"
	ErrorTypeAuthentication ErrorType = "authentication_error"
	ErrorTypePermission     ErrorType = "permission_error"
	ErrorTypeNotFound       ErrorType = "not_found_error"
	ErrorTypeRateLimit      ErrorType = "rate_limit_error"
	ErrorTypeAPI            ErrorType = "api_error"
	ErrorTypeOverloaded     ErrorType = "overloaded_error"
)

// APIError represents an error from the Claude API.
type APIError struct {
	Type         string        `json:"type"` // Always "error"
	ErrorDetails ErrorDetail   `json:"error"`
	StatusCode   int           `json:"-"` // HTTP status code (not from JSON)
	RetryAfter   time.Duration `json:"-"` // Retry-After header value (not from JSON)
}

// ErrorDetail contains the error details.
type ErrorDetail struct {
	Type    ErrorType `json:"type"`
	Message string    `json:"message"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return string(e.ErrorDetails.Type) + ": " + e.ErrorDetails.Message
}

// IsRateLimited returns true if this is a rate limit error.
func (e *APIError) IsRateLimited() bool {
	return e.ErrorDetails.Type == ErrorTypeRateLimit
}

// IsOverloaded returns true if this is an overloaded error.
func (e *APIError) IsOverloaded() bool {
	return e.ErrorDetails.Type == ErrorTypeOverloaded
}

// IsRetryable returns true if the error is potentially retryable.
func (e *APIError) IsRetryable() bool {
	return e.IsRateLimited() || e.IsOverloaded() || e.ErrorDetails.Type == ErrorTypeAPI
}

// ----------------------------------------------------------------------------
// Streaming Event Types
// ----------------------------------------------------------------------------

// StreamEventType identifies the type of a streaming event.
type StreamEventType string

const (
	StreamEventMessageStart      StreamEventType = "message_start"
	StreamEventContentBlockStart StreamEventType = "content_block_start"
	StreamEventContentBlockDelta StreamEventType = "content_block_delta"
	StreamEventContentBlockStop  StreamEventType = "content_block_stop"
	StreamEventMessageDelta      StreamEventType = "message_delta"
	StreamEventMessageStop       StreamEventType = "message_stop"
	StreamEventPing              StreamEventType = "ping"
	StreamEventError             StreamEventType = "error"
)

// StreamEvent represents a server-sent event during streaming.
type StreamEvent struct {
	Type  StreamEventType `json:"type"`
	Index *int            `json:"index,omitempty"`

	// message_start
	Message *Response `json:"message,omitempty"`

	// content_block_start
	ContentBlock *ContentBlock `json:"content_block,omitempty"`

	// content_block_delta
	Delta *StreamDelta `json:"delta,omitempty"`

	// message_delta
	Usage *Usage `json:"usage,omitempty"`

	// error
	Error *ErrorDetail `json:"error,omitempty"`
}

// StreamDelta contains the delta content in a streaming event.
type StreamDelta struct {
	Type string `json:"type"`

	// text_delta
	Text string `json:"text,omitempty"`

	// input_json_delta (for tool use)
	PartialJSON string `json:"partial_json,omitempty"`

	// thinking_delta
	Thinking string `json:"thinking,omitempty"`

	// message_delta fields
	StopReason   StopReason `json:"stop_reason,omitempty"`
	StopSequence *string    `json:"stop_sequence,omitempty"`
}

// ----------------------------------------------------------------------------
// Model Constants
// ----------------------------------------------------------------------------

// Common model identifiers.
const (
	ModelOpus45         = "claude-opus-4-5-20251101"
	ModelOpus45Latest   = "claude-opus-4-5"
	ModelSonnet45       = "claude-sonnet-4-5-20250929"
	ModelSonnet45Latest = "claude-sonnet-4-5"
	ModelSonnet4        = "claude-sonnet-4-20250514"
	ModelHaiku45        = "claude-haiku-4-5-20251001"
	ModelHaiku45Latest  = "claude-haiku-4-5"
	ModelHaiku35        = "claude-3-5-haiku-20241022"
	ModelHaiku35Latest  = "claude-3-5-haiku-latest"
)

// ----------------------------------------------------------------------------
// Models API
// ----------------------------------------------------------------------------

// ModelInfo represents a model returned by the Models API.
type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
	Type        string `json:"type"` // Always "model"
}

// ModelsListResponse is the response from GET /v1/models.
type ModelsListResponse struct {
	Data    []ModelInfo `json:"data"`
	HasMore bool       `json:"has_more"`
	FirstID string     `json:"first_id"`
	LastID  string     `json:"last_id"`
}
