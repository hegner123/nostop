// Package nostop provides the main Nostop engine for intelligent topic-based context archival.
package nostop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hegner123/nostop/internal/api"
	"github.com/hegner123/nostop/internal/storage"
	"github.com/hegner123/nostop/internal/tools"
	"github.com/hegner123/nostop/internal/topic"
)

// Config contains configuration for the Nostop engine.
type Config struct {
	// MaxContextTokens is the model's context limit (e.g., 200000).
	MaxContextTokens int

	// ArchiveThreshold is the usage percentage that triggers archival (default: 0.95).
	ArchiveThreshold float64

	// ArchiveTarget is the usage percentage to archive down to (default: 0.50).
	ArchiveTarget float64

	// DetectionModel is the model used for topic detection (default: haiku).
	DetectionModel string

	// ChatModel is the model used for chat responses (default: sonnet).
	ChatModel string

	// APIKey is the Anthropic API key.
	APIKey string

	// DBPath is the path to the SQLite database.
	DBPath string

	// CacheStrategy defines how to cache API requests for cost optimization.
	// If nil, uses DefaultCacheStrategy() with caching enabled.
	CacheStrategy *CacheStrategy

	// ToolsEnabled enables the agentic tool-use loop in Send/SendStream.
	ToolsEnabled bool

	// DisabledTools lists tool names to exclude from the registry.
	DisabledTools []string

	// ToolWorkDir is the working directory for tool subprocess execution.
	ToolWorkDir string

	// ToolTimeout is the per-tool execution timeout.
	ToolTimeout time.Duration
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxContextTokens: 200000,
		ArchiveThreshold: ThresholdArchive,
		ArchiveTarget:    ArchiveTarget,
		DetectionModel:   api.ModelHaiku45Latest,
		ChatModel:        api.ModelOpus45Latest,
	}
}

// Nostop is the main orchestrator for the topic-based context archival system.
type Nostop struct {
	client        *api.Client
	storage       *storage.SQLite
	detector      *topic.TopicDetector
	tracker       *topic.TopicTracker
	scorer        *topic.TopicScorer
	context       *ContextManager
	archiver      *Archiver
	config        Config
	cacheStrategy CacheStrategy
	cacheStats    *CacheStats

	// User prompt loaded from .claude/PROMPT.md files at startup.
	// Included in every conversation's system prompt.
	userPrompt string

	// Agentic tool-use fields
	toolRegistry *tools.Registry
	toolExecutor *tools.Executor
	toolsEnabled bool

	mu sync.RWMutex
	wg sync.WaitGroup // tracks background goroutines (e.g., relevance re-scoring)
}

// ToolEventType describes the phase of a tool invocation.
type ToolEventType int

const (
	ToolEventStart ToolEventType = iota
	ToolEventDone
	ToolEventError
)

// ToolEvent is emitted during the agentic loop to notify callers about tool execution.
type ToolEvent struct {
	Type      ToolEventType
	Name      string
	ID        string
	Input     map[string]any
	Output    string
	IsError   bool
	Iteration int
}

// ToolCallback is called during SendStream to report tool execution events.
type ToolCallback func(event ToolEvent)

// agenticSystemPrompt is prepended to the conversation's system prompt when
// tools are enabled. It frames Claude as an agent that uses tools proactively
// rather than describing what it would do.
const agenticSystemPrompt = `You are an agentic coding assistant. You MUST use your tools to fulfill requests. Never describe what you would do — do it by calling tools.

IMPORTANT: You have tools available. Use them on every turn. If the user asks you to read a file, call the "read" tool. If they ask you to search, call "checkfor". If they ask you to run something, call "bash". Do not explain what you could do — just do it.

Your tools:
- read: Read file contents. Call this to see any file.
- write: Create or modify files. You must read a file before writing to it.
- bash: Execute shell commands (builds, tests, git, anything).
- checkfor: Search for text in files across directories.
- repfor: Search and replace text across files.
- stump: Show directory tree structure.
- sig: Extract function signatures and types from source files.
- imports: Map dependencies and imports in a directory.
- cleanDiff: Show git diff as structured JSON.
- errs: Parse compiler/linter error output.
- tabcount: Count tab indentation per line.
- notab: Convert tabs to spaces or vice versa.
- split: Split a file into parts at line numbers.
- splice: Insert file contents into another file.
- delete: Move files to Trash safely.
- conflicts: Parse git merge conflict markers.
- transform: Process JSON data through a pipeline.
- utf8: Fix corrupted file encoding.

Rules:
- Always call a tool when you can. Text-only responses should be rare.
- Read files before writing to them.
- Use bash for builds, tests, and git commands.
- When exploring code: stump for structure, sig for APIs, checkfor for searching.
- Act incrementally: change, verify, continue.`

// buildSystemPrompt composes the final system prompt from three layers:
//   1. Agentic preamble (when tools are enabled)
//   2. User prompt (from .claude/PROMPT.md, loaded at startup)
//   3. Conversation-specific prompt (passed to NewConversation)
func (r *Nostop) buildSystemPrompt(convSystemPrompt string) string {
	var parts []string

	if r.toolsEnabled {
		parts = append(parts, agenticSystemPrompt)
	}

	if r.userPrompt != "" {
		parts = append(parts, r.userPrompt)
	}

	if convSystemPrompt != "" {
		parts = append(parts, convSystemPrompt)
	}

	return strings.Join(parts, "\n\n")
}

// Response is the result of sending a message through the Nostop engine.
type Response struct {
	// Content is the assistant's response text.
	Content string `json:"content"`

	// TokensUsed is the total tokens used for this request.
	TokensUsed int `json:"tokens_used"`

	// TopicShift is non-nil if a topic shift was detected.
	TopicShift *topic.TopicShift `json:"topic_shift,omitempty"`

	// Archived contains topics that were archived during this turn.
	Archived []storage.Topic `json:"archived,omitempty"`

	// Usage contains the raw usage information from the API.
	Usage api.Usage `json:"usage"`

	// MessageID is the ID of the stored assistant message.
	MessageID string `json:"message_id"`

	// CacheStats contains cache usage information for this request.
	// This is populated when caching is enabled.
	CacheStats *CacheStats `json:"cache_stats,omitempty"`
}

// Conversation is an alias for storage.Conversation for public API exposure.
type Conversation = storage.Conversation

// New creates a new Nostop engine instance with all components initialized.
func New(cfg Config) (*Nostop, error) {
	// Validate config
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	if cfg.DBPath == "" {
		return nil, fmt.Errorf("database path is required")
	}

	// Apply defaults
	if cfg.MaxContextTokens == 0 {
		cfg.MaxContextTokens = 200000
	}
	if cfg.ArchiveThreshold == 0 {
		cfg.ArchiveThreshold = ThresholdArchive
	}
	if cfg.ArchiveTarget == 0 {
		cfg.ArchiveTarget = ArchiveTarget
	}
	if cfg.DetectionModel == "" {
		cfg.DetectionModel = api.ModelHaiku45Latest
	}
	if cfg.ChatModel == "" {
		cfg.ChatModel = api.ModelOpus45Latest
	}

	// Initialize API client with model fallback so deprecated model IDs
	// (e.g., from stale config files) automatically resolve to current ones.
	client := api.NewClient(cfg.APIKey, api.WithModelResolver(api.NewModelResolver()))

	// Initialize storage
	store, err := storage.NewSQLite(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	// Initialize schema
	if err := store.InitSchema(); err != nil {
		store.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	// Initialize topic components
	detector := topic.NewTopicDetector(client, cfg.DetectionModel)
	tracker := topic.NewTopicTracker(store)
	scorer := topic.NewTopicScorer(client, cfg.DetectionModel)

	// Initialize context manager
	ctxMgr := NewContextManager(cfg.MaxContextTokens, tracker, client, cfg.ChatModel)

	// Initialize archiver
	archiver := NewArchiver(store, tracker)

	// Initialize cache strategy
	var cacheStrategy CacheStrategy
	if cfg.CacheStrategy != nil {
		cacheStrategy = *cfg.CacheStrategy
	} else {
		cacheStrategy = DefaultCacheStrategy()
	}

	// Initialize tool registry if tools are enabled
	var toolReg *tools.Registry
	var toolExec *tools.Executor
	toolsEnabled := cfg.ToolsEnabled
	if toolsEnabled {
		toolReg = tools.DefaultRegistry()
		for _, name := range cfg.DisabledTools {
			toolReg.Remove(name)
		}
		toolExec = tools.NewExecutor(toolReg, cfg.ToolWorkDir)
	}

	// Load user prompt from .claude/PROMPT.md files
	userPrompt := LoadUserPrompt()
	if userPrompt != "" {
		log.Printf("[prompt] Loaded user prompt (%d bytes)", len(userPrompt))
	}

	return &Nostop{
		client:        client,
		storage:       store,
		detector:      detector,
		tracker:       tracker,
		scorer:        scorer,
		context:       ctxMgr,
		archiver:      archiver,
		config:        cfg,
		cacheStrategy: cacheStrategy,
		cacheStats:    NewCacheStats(),
		userPrompt:    userPrompt,
		toolRegistry:  toolReg,
		toolExecutor:  toolExec,
		toolsEnabled:  toolsEnabled,
	}, nil
}

// Send sends a user message and returns the assistant's response.
// Like SendStream, the lock is split into pre-send and post-send phases
// so that readers are not blocked during the API call.
func (r *Nostop) Send(ctx context.Context, convID, message string) (*Response, error) {
	// ── Phase 1: pre-send setup (write lock) ────────────────────────────
	conv, currentTopic, userMsg, topicShift, archived, req, err := r.prepareSend(ctx, convID, message)
	if err != nil {
		return nil, err
	}

	// ── Phase 2: API call + tool loop (no lock) ─────────────────────────
	finalResp, requestCacheStats, err := r.executeSend(ctx, req)
	if err != nil {
		return nil, err
	}

	// ── Phase 3: store response (write lock) ────────────────────────────
	responseText := finalResp.GetText()

	r.mu.Lock()
	assistantMsg, err := r.createMessage(ctx, conv, currentTopic, storage.RoleAssistant, responseText)
	if err != nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("failed to store assistant message: %w", err)
	}
	_ = r.updateTokenCounts(ctx, conv, currentTopic, userMsg, assistantMsg, finalResp.Usage)
	r.mu.Unlock()

	return &Response{
		Content:    responseText,
		TokensUsed: finalResp.Usage.InputTokens + finalResp.Usage.OutputTokens,
		TopicShift: topicShift,
		Archived:   archived,
		Usage:      finalResp.Usage,
		MessageID:  assistantMsg.ID,
		CacheStats: requestCacheStats,
	}, nil
}

// prepareSend runs under the write lock. Returns all values needed for the API call.
func (r *Nostop) prepareSend(ctx context.Context, convID, message string) (
	conv *storage.Conversation,
	currentTopic *storage.Topic,
	userMsg *storage.Message,
	topicShift *topic.TopicShift,
	archived []storage.Topic,
	req *api.Request,
	err error,
) {
	r.mu.Lock()
	defer r.mu.Unlock()

	conv, err = r.storage.GetConversation(ctx, convID)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	if conv == nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("conversation not found: %s", convID)
	}

	if err = r.tracker.LoadTopics(ctx, convID); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to load topics: %w", err)
	}

	currentTopic = r.tracker.GetCurrentTopic()
	if currentTopic == nil {
		currentTopic, err = r.createInitialTopic(ctx, conv, message)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to create initial topic: %w", err)
		}
	}

	userMsg, err = r.createMessage(ctx, conv, currentTopic, storage.RoleUser, message)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to store user message: %w", err)
	}

	topicShift, _ = r.detectTopicShift(ctx, convID, currentTopic)
	if topicShift != nil && topicShift.Detected {
		newTopic, shiftErr := r.handleTopicShift(ctx, conv, topicShift, userMsg)
		if shiftErr != nil {
			topicShift = nil
		} else {
			currentTopic = newTopic
		}
	}

	usage, err := r.context.GetUsage(ctx, conv)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to get context usage: %w", err)
	}

	if r.context.ShouldArchive(usage) {
		archived, err = r.archiver.ArchiveUntilTarget(
			ctx, convID, usage.UsagePercent,
			r.config.MaxContextTokens, r.config.ArchiveTarget,
		)
		if err != nil && !errors.Is(err, ErrNoTopicsToArchive) {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to archive topics: %w", err)
		}
	}

	messages, err := r.BuildContext(ctx, conv)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to build context: %w", err)
	}

	req = &api.Request{
		Model:     r.config.ChatModel,
		MaxTokens: 8192,
		Messages:  messages,
	}

	systemPrompt := r.buildSystemPrompt(conv.SystemPrompt)
	if systemPrompt != "" {
		req.System = r.cacheStrategy.ApplyToSystemPrompt(systemPrompt)
	}

	if r.toolsEnabled && r.toolRegistry != nil {
		req.Tools = r.toolRegistry.APITools()
	}

	return conv, currentTopic, userMsg, topicShift, archived, req, nil
}

// executeSend runs the API call and agentic tool loop WITHOUT the engine lock.
func (r *Nostop) executeSend(ctx context.Context, req *api.Request) (*api.Response, *CacheStats, error) {
	agentLog := tools.GetAgentLogger()
	apiMessages := req.Messages
	requestCacheStats := NewCacheStats()
	var finalResp *api.Response

	for iteration := 0; ; iteration++ {
		req.Messages = apiMessages
		agentLog.LogIteration(iteration, len(apiMessages))

		resp, sendErr := r.client.Send(ctx, req)
		if sendErr != nil {
			return nil, nil, fmt.Errorf("API call failed (iteration %d): %w", iteration, sendErr)
		}

		requestCacheStats.Update(resp.Usage)
		r.cacheStats.Update(resp.Usage)

		if !resp.HasToolUse() {
			finalResp = resp
			agentLog.LogResponse(iteration+1, false, len(resp.GetText()))
			break
		}

		if !r.toolsEnabled || r.toolExecutor == nil {
			finalResp = resp
			agentLog.LogResponse(iteration+1, true, len(resp.GetText()))
			break
		}

		apiMessages = append(apiMessages, assistantMessageFromResponse(resp))

		var resultBlocks []any
		for _, block := range resp.GetToolUses() {
			var input map[string]any
			if unmarshalErr := json.Unmarshal(block.Input, &input); unmarshalErr != nil {
				resultBlocks = append(resultBlocks, api.NewToolResultBlockError(block.ID,
					fmt.Sprintf("failed to parse tool input: %s", unmarshalErr)))
				continue
			}

			agentLog.LogToolCall(block.Name, block.ID, input, iteration)

			result := r.toolExecutor.Execute(ctx, block.Name, input)
			if result.IsError {
				resultBlocks = append(resultBlocks, api.NewToolResultBlockError(block.ID, result.Error))
			} else {
				resultBlocks = append(resultBlocks, api.NewToolResultBlock(block.ID, result.Output))
			}
		}

		apiMessages = append(apiMessages, api.MessageParam{
			Role:    api.RoleUser,
			Content: resultBlocks,
		})
	}

	return finalResp, requestCacheStats, nil
}

// SendStream sends a user message and streams the response via callback.
// If toolCallback is non-nil, tool execution events are reported through it.
//
// The lock is held only during state mutations (phases 1 and 3), not during
// the streaming HTTP call or tool execution. This allows the debug view,
// topics view, and other readers to query state while streaming is in progress.
func (r *Nostop) SendStream(ctx context.Context, convID, message string, callback api.StreamCallback, toolCallback ToolCallback) error {
	// ── Phase 1: pre-stream setup (write lock) ──────────────────────────
	// Mutates: topics, messages, archival state.
	// Captures everything needed for the API call into local variables.
	conv, currentTopic, userMsg, req, err := r.prepareStream(ctx, convID, message)
	if err != nil {
		return err
	}

	// ── Phase 2: stream + tool loop (no lock) ───────────────────────────
	// Pure I/O: HTTP streaming, tool subprocess execution.
	// Only touches local variables, the HTTP client (stateless), the tool
	// executor (has its own synchronization), and cacheStats (now has its
	// own mutex).
	finalResp, err := r.executeStream(ctx, req, callback, toolCallback)
	if err != nil {
		return err
	}

	// ── Phase 3: post-stream finalization (write lock) ──────────────────
	// Mutates: messages (store response), token counts.
	return r.finalizeStream(ctx, conv, currentTopic, userMsg, finalResp)
}

// prepareStream runs under the write lock. It validates the conversation,
// manages topics, stores the user message, checks for archival, and builds
// the API request. Returns all values needed for the streaming phase.
func (r *Nostop) prepareStream(ctx context.Context, convID, message string) (
	conv *storage.Conversation,
	currentTopic *storage.Topic,
	userMsg *storage.Message,
	req *api.Request,
	err error,
) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get or verify conversation exists
	conv, err = r.storage.GetConversation(ctx, convID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	if conv == nil {
		return nil, nil, nil, nil, fmt.Errorf("conversation not found: %s", convID)
	}

	// Load topics for this conversation
	if err = r.tracker.LoadTopics(ctx, convID); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to load topics: %w", err)
	}

	// Get or create current topic
	currentTopic = r.tracker.GetCurrentTopic()
	if currentTopic == nil {
		currentTopic, err = r.createInitialTopic(ctx, conv, message)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to create initial topic: %w", err)
		}
	}

	// Create and store user message
	userMsg, err = r.createMessage(ctx, conv, currentTopic, storage.RoleUser, message)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to store user message: %w", err)
	}

	// Check for topic shift
	topicShift, _ := r.detectTopicShift(ctx, convID, currentTopic)
	if topicShift != nil && topicShift.Detected {
		newTopic, shiftErr := r.handleTopicShift(ctx, conv, topicShift, userMsg)
		if shiftErr == nil {
			currentTopic = newTopic
		}
	}

	// Calculate context usage and archive if needed
	usage, err := r.context.GetUsage(ctx, conv)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to get context usage: %w", err)
	}

	if r.context.ShouldArchive(usage) {
		_, err = r.archiver.ArchiveUntilTarget(
			ctx,
			convID,
			usage.UsagePercent,
			r.config.MaxContextTokens,
			r.config.ArchiveTarget,
		)
		if err != nil && !errors.Is(err, ErrNoTopicsToArchive) {
			return nil, nil, nil, nil, fmt.Errorf("failed to archive topics: %w", err)
		}
	}

	// Build context (includes the user message stored above)
	messages, err := r.BuildContext(ctx, conv)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to build context: %w", err)
	}

	// Prepare request
	req = &api.Request{
		Model:     r.config.ChatModel,
		MaxTokens: 8192,
		Messages:  messages,
	}

	// Build system prompt
	systemPrompt := r.buildSystemPrompt(conv.SystemPrompt)
	if systemPrompt != "" {
		req.System = r.cacheStrategy.ApplyToSystemPrompt(systemPrompt)
	}

	// Add tools to request if enabled
	if r.toolsEnabled && r.toolRegistry != nil {
		req.Tools = r.toolRegistry.APITools()
	}

	return conv, currentTopic, userMsg, req, nil
}

// executeStream runs the streaming API call and agentic tool loop WITHOUT
// holding the engine lock. All state accessed here is either local, stateless
// (HTTP client), or independently synchronized (cacheStats, toolExecutor).
func (r *Nostop) executeStream(ctx context.Context, req *api.Request, callback api.StreamCallback, toolCallback ToolCallback) (*api.Response, error) {
	streamLog := tools.GetAgentLogger()
	apiMessages := req.Messages
	var finalResp *api.Response

	for iteration := 0; ; iteration++ {
		req.Messages = apiMessages
		streamLog.LogIteration(iteration, len(apiMessages))

		resp, streamErr := r.client.StreamWithCallback(ctx, req, callback)
		if streamErr != nil {
			return nil, fmt.Errorf("stream failed (iteration %d): %w", iteration, streamErr)
		}

		r.cacheStats.Update(resp.Usage)

		// If no tool use, this is the final response
		if !resp.HasToolUse() {
			finalResp = resp
			streamLog.LogResponse(iteration+1, false, len(resp.GetText()))
			break
		}

		// Tools not enabled — treat as final response
		if !r.toolsEnabled || r.toolExecutor == nil {
			finalResp = resp
			streamLog.LogResponse(iteration+1, true, len(resp.GetText()))
			break
		}

		// Append assistant message with tool_use blocks
		apiMessages = append(apiMessages, assistantMessageFromResponse(resp))

		// Execute each tool and collect results
		var resultBlocks []any
		for _, block := range resp.GetToolUses() {
			var input map[string]any
			if unmarshalErr := json.Unmarshal(block.Input, &input); unmarshalErr != nil {
				resultBlocks = append(resultBlocks, api.NewToolResultBlockError(block.ID,
					fmt.Sprintf("failed to parse tool input: %s", unmarshalErr)))
				continue
			}

			streamLog.LogToolCall(block.Name, block.ID, input, iteration)

			// Notify caller about tool execution start
			if toolCallback != nil {
				toolCallback(ToolEvent{
					Type:      ToolEventStart,
					Name:      block.Name,
					ID:        block.ID,
					Input:     input,
					Iteration: iteration,
				})
			}

			result := r.toolExecutor.Execute(ctx, block.Name, input)

			// Notify caller about tool result
			if toolCallback != nil {
				evtType := ToolEventDone
				if result.IsError {
					evtType = ToolEventError
				}
				output := result.Output
				if result.IsError {
					output = result.Error
				}
				toolCallback(ToolEvent{
					Type:      evtType,
					Name:      block.Name,
					ID:        block.ID,
					Output:    output,
					IsError:   result.IsError,
					Iteration: iteration,
				})
			}

			if result.IsError {
				resultBlocks = append(resultBlocks, api.NewToolResultBlockError(block.ID, result.Error))
			} else {
				resultBlocks = append(resultBlocks, api.NewToolResultBlock(block.ID, result.Output))
			}
		}

		// Append user message with tool_result blocks
		apiMessages = append(apiMessages, api.MessageParam{
			Role:    api.RoleUser,
			Content: resultBlocks,
		})
	}

	return finalResp, nil
}

// finalizeStream runs under the write lock. It stores the assistant response
// and updates token counts.
func (r *Nostop) finalizeStream(ctx context.Context, conv *storage.Conversation, currentTopic *storage.Topic, userMsg *storage.Message, finalResp *api.Response) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	responseText := finalResp.GetText()
	assistantMsg, err := r.createMessage(ctx, conv, currentTopic, storage.RoleAssistant, responseText)
	if err != nil {
		return fmt.Errorf("failed to store assistant message: %w", err)
	}

	_ = r.updateTokenCounts(ctx, conv, currentTopic, userMsg, assistantMsg, finalResp.Usage)
	return nil
}

// BuildContext constructs the message context from active (non-archived) topics.
// It respects topic allocation percentages and includes the system prompt.
func (r *Nostop) BuildContext(ctx context.Context, conv *storage.Conversation) ([]api.MessageParam, error) {
	// Get all active messages for this conversation
	messages, err := r.storage.ListActiveMessages(ctx, conv.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active messages: %w", err)
	}

	// Convert storage messages to API message params
	var apiMessages []api.MessageParam
	for _, msg := range messages {
		var role api.Role
		if msg.Role == storage.RoleUser {
			role = api.RoleUser
		} else {
			role = api.RoleAssistant
		}

		apiMessages = append(apiMessages, api.MessageParam{
			Role:    role,
			Content: msg.Content,
		})
	}

	return apiMessages, nil
}

// NewConversation creates a new conversation.
func (r *Nostop) NewConversation(ctx context.Context, title, systemPrompt string) (*Conversation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	conv := &storage.Conversation{
		ID:           uuid.New().String(),
		Title:        title,
		Model:        r.config.ChatModel,
		SystemPrompt: systemPrompt,
	}

	if err := r.storage.CreateConversation(ctx, conv); err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	return conv, nil
}

// GetConversation retrieves a conversation by ID.
func (r *Nostop) GetConversation(ctx context.Context, convID string) (*Conversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conv, err := r.storage.GetConversation(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	return conv, nil
}

// ListConversations retrieves all conversations.
func (r *Nostop) ListConversations(ctx context.Context) ([]Conversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Use a large limit for "all" conversations
	convs, err := r.storage.ListConversations(ctx, 1000, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	return convs, nil
}

// ListConversationsPaginated retrieves conversations with pagination.
func (r *Nostop) ListConversationsPaginated(ctx context.Context, limit, offset int) ([]Conversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	convs, err := r.storage.ListConversations(ctx, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	return convs, nil
}

// GetContextUsage returns the current context usage for a conversation.
func (r *Nostop) GetContextUsage(ctx context.Context, convID string) (*ContextUsage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conv, err := r.storage.GetConversation(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	if conv == nil {
		return nil, fmt.Errorf("conversation not found: %s", convID)
	}

	if err := r.tracker.LoadTopics(ctx, convID); err != nil {
		return nil, fmt.Errorf("failed to load topics: %w", err)
	}

	return r.context.GetUsage(ctx, conv)
}

// GetTopics returns all topics for a conversation.
func (r *Nostop) GetTopics(ctx context.Context, convID string) ([]storage.Topic, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.storage.ListTopics(ctx, convID)
}

// GetActiveTopics returns only active (non-archived) topics.
func (r *Nostop) GetActiveTopics(ctx context.Context, convID string) ([]storage.Topic, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.storage.GetActiveTopics(ctx, convID)
}

// GetArchivedTopics returns archived topics for a conversation.
func (r *Nostop) GetArchivedTopics(ctx context.Context, convID string) ([]storage.Topic, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.archiver.GetArchivedTopics(ctx, convID)
}

// RestoreTopic restores an archived topic to active context.
func (r *Nostop) RestoreTopic(ctx context.Context, topicID string) (*storage.Topic, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get the topic to find its conversation
	t, err := r.storage.GetTopic(ctx, topicID)
	if err != nil {
		return nil, fmt.Errorf("failed to get topic: %w", err)
	}
	if t == nil {
		return nil, ErrTopicNotFound
	}

	// Get current usage
	conv, err := r.storage.GetConversation(ctx, t.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	if err := r.tracker.LoadTopics(ctx, t.ConversationID); err != nil {
		return nil, fmt.Errorf("failed to load topics: %w", err)
	}

	usage, err := r.context.GetUsage(ctx, conv)
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	// Calculate usage after restore
	usageAfter := usage.UsagePercent + (float64(t.TokenCount) / float64(r.config.MaxContextTokens))

	return r.archiver.RestoreTopic(ctx, topicID, usage.UsagePercent, usageAfter)
}

// Close closes the Nostop engine and releases resources.
func (r *Nostop) Close() error {
	// Wait for background goroutines (e.g., relevance re-scoring) to finish
	// before closing storage, so they don't write to a closed DB.
	r.wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.storage != nil {
		return r.storage.Close()
	}
	return nil
}

// Client returns the underlying API client.
func (r *Nostop) Client() *api.Client {
	return r.client
}

// Storage returns the underlying storage.
func (r *Nostop) Storage() *storage.SQLite {
	return r.storage
}

// Tracker returns the topic tracker used by the engine.
func (r *Nostop) Tracker() *topic.TopicTracker {
	return r.tracker
}

// ContextMgr returns the context manager used by the engine.
func (r *Nostop) ContextMgr() *ContextManager {
	return r.context
}

// Archiver returns the archiver used by the engine.
func (r *Nostop) InternalArchiver() *Archiver {
	return r.archiver
}

// Config returns the current configuration.
func (r *Nostop) Config() Config {
	return r.config
}

// CacheStrategy returns the current cache strategy.
func (r *Nostop) CacheStrategy() CacheStrategy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cacheStrategy
}

// SetCacheStrategy updates the cache strategy.
func (r *Nostop) SetCacheStrategy(strategy CacheStrategy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cacheStrategy = strategy
}

// CacheStats returns a copy of the cumulative cache statistics.
func (r *Nostop) CacheStats() *CacheStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cacheStats.Clone()
}

// ResetCacheStats resets the cumulative cache statistics.
func (r *Nostop) ResetCacheStats() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cacheStats.Reset()
}

// ToolsEnabled returns whether agentic tool use is active.
func (r *Nostop) ToolsEnabled() bool {
	return r.toolsEnabled
}

// ToolRegistry returns the tool registry, or nil if tools are disabled.
func (r *Nostop) ToolRegistry() *tools.Registry {
	return r.toolRegistry
}

// CacheDebugInfo returns debug information about caching for display.
func (r *Nostop) CacheDebugInfo() *CacheDebugInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return NewCacheDebugInfo(r.cacheStrategy, r.cacheStats.Clone())
}

// --- Internal helpers ---

// assistantMessageFromResponse converts an API response into a MessageParam
// suitable for appending to the conversation as an assistant turn.
// This preserves both text and tool_use blocks for the agentic loop.
func assistantMessageFromResponse(resp *api.Response) api.MessageParam {
	var blocks []any
	for _, block := range resp.Content {
		switch {
		case block.IsText():
			blocks = append(blocks, api.TextBlockParam{
				Type: api.ContentBlockTypeText,
				Text: block.Text,
			})
		case block.IsToolUse():
			var input map[string]any
			if err := json.Unmarshal(block.Input, &input); err != nil {
				input = make(map[string]any)
			}
			blocks = append(blocks, api.ToolUseBlockParam{
				Type:  api.ContentBlockTypeToolUse,
				ID:    block.ID,
				Name:  block.Name,
				Input: input,
			})
		}
	}
	return api.MessageParam{
		Role:    api.RoleAssistant,
		Content: blocks,
	}
}

// createInitialTopic creates the first topic for a conversation.
func (r *Nostop) createInitialTopic(ctx context.Context, conv *storage.Conversation, initialMessage string) (*storage.Topic, error) {
	// Use detector to identify the topic
	msg := topic.Message{
		ID:      uuid.New().String(),
		Role:    "user",
		Content: initialMessage,
	}

	detected, err := r.detector.IdentifyTopic(ctx, []topic.Message{msg})
	if err != nil {
		log.Printf("[topic] IdentifyTopic failed (conv %s): %v — falling back to generic topic", conv.ID, err)
		// Fall back to generic topic
		detected = &topic.Topic{
			Name:     "Initial conversation",
			Keywords: []string{},
		}
	} else {
		log.Printf("[topic] Identified topic %q with keywords %v (conv %s)", detected.Name, detected.Keywords, conv.ID)
	}

	storageTopic := &storage.Topic{
		ID:             uuid.New().String(),
		ConversationID: conv.ID,
		Name:           detected.Name,
		Keywords:       detected.Keywords,
		RelevanceScore: 1.0,
		IsCurrent:      true,
	}

	if err := r.tracker.AddTopic(ctx, storageTopic); err != nil {
		return nil, fmt.Errorf("failed to add topic: %w", err)
	}

	if err := r.tracker.SetCurrentTopic(ctx, storageTopic.ID); err != nil {
		return nil, fmt.Errorf("failed to set current topic: %w", err)
	}

	log.Printf("[topic] Created topic %q (ID: %s) for conv %s", storageTopic.Name, storageTopic.ID, conv.ID)
	return storageTopic, nil
}

// detectTopicShift checks if the conversation has shifted to a new topic.
func (r *Nostop) detectTopicShift(ctx context.Context, convID string, currentTopic *storage.Topic) (*topic.TopicShift, error) {
	// Get recent messages for analysis
	messages, err := r.storage.ListActiveMessages(ctx, convID)
	if err != nil {
		return nil, err
	}

	// Take last 5 messages
	start := len(messages) - 5
	if start < 0 {
		start = 0
	}
	recentMessages := messages[start:]

	if len(recentMessages) < 2 {
		return nil, nil
	}

	// Convert to topic.Message
	var topicMessages []topic.Message
	for _, m := range recentMessages {
		topicMessages = append(topicMessages, topic.Message{
			ID:      m.ID,
			Role:    string(m.Role),
			Content: m.Content,
		})
	}

	// Convert current topic
	var currentTopicForDetection *topic.Topic
	if currentTopic != nil {
		currentTopicForDetection = &topic.Topic{
			ID:       currentTopic.ID,
			Name:     currentTopic.Name,
			Keywords: currentTopic.Keywords,
		}
	}

	shift, err := r.detector.DetectTopicShift(ctx, topicMessages, currentTopicForDetection)
	if err != nil {
		log.Printf("[topic] DetectTopicShift failed (conv %s): %v", convID, err)
		return nil, err
	}
	if shift != nil && shift.Detected {
		log.Printf("[topic] Topic shift detected (conv %s): %q → %q (confidence: %.2f, reason: %s)",
			convID, currentTopic.Name, shift.NewTopicName, shift.Confidence, shift.Reason)
	}

	return shift, nil
}

// handleTopicShift creates a new topic when a shift is detected.
func (r *Nostop) handleTopicShift(ctx context.Context, conv *storage.Conversation, shift *topic.TopicShift, triggerMsg *storage.Message) (*storage.Topic, error) {
	// Create new topic
	newTopic := &storage.Topic{
		ID:             uuid.New().String(),
		ConversationID: conv.ID,
		Name:           shift.NewTopicName,
		Keywords:       shift.NewKeywords,
		RelevanceScore: 1.0,
		IsCurrent:      true,
	}

	// Add the new topic
	if err := r.tracker.AddTopic(ctx, newTopic); err != nil {
		return nil, fmt.Errorf("failed to add new topic: %w", err)
	}

	// Set as current
	if err := r.tracker.SetCurrentTopic(ctx, newTopic.ID); err != nil {
		return nil, fmt.Errorf("failed to set current topic: %w", err)
	}

	// Re-score old topics based on relevance to new topic.
	// Capture topics and new topic name before launching goroutine to avoid
	// racing on tracker state after the caller releases the mutex.
	topicsSnapshot := r.tracker.GetTopics()
	newTopicName := shift.NewTopicName
	newTopicID := newTopic.ID

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		bgCtx := context.Background()
		for _, t := range topicsSnapshot {
			if t.ID == newTopicID {
				continue
			}

			score, err := r.scorer.ScoreRelevance(bgCtx, topic.Topic{
				ID:       t.ID,
				Name:     t.Name,
				Keywords: t.Keywords,
			}, newTopicName)
			if err != nil {
				continue
			}

			r.mu.Lock()
			_ = r.tracker.UpdateRelevance(bgCtx, t.ID, score)
			r.mu.Unlock()
		}
	}()

	return newTopic, nil
}

// createMessage creates and stores a message.
func (r *Nostop) createMessage(ctx context.Context, conv *storage.Conversation, t *storage.Topic, role storage.Role, content string) (*storage.Message, error) {
	var topicID *string
	if t != nil {
		topicID = &t.ID
	}

	msg := &storage.Message{
		ID:             uuid.New().String(),
		ConversationID: conv.ID,
		TopicID:        topicID,
		Role:           role,
		Content:        content,
	}

	if err := r.storage.CreateMessage(ctx, msg); err != nil {
		return nil, err
	}

	return msg, nil
}

// updateTokenCounts updates token counts for messages, topics, and conversation.
func (r *Nostop) updateTokenCounts(ctx context.Context, conv *storage.Conversation, t *storage.Topic, userMsg, assistantMsg *storage.Message, usage api.Usage) error {
	// Estimate tokens for each message
	userTokens, err := r.context.EstimateTokens(ctx, []api.MessageParam{
		api.NewUserMessage(userMsg.Content),
	})
	if err != nil {
		userTokens = len(userMsg.Content) / 4
	}

	assistantTokens := usage.OutputTokens

	// Update user message token count
	userMsg.TokenCount = userTokens
	if err := r.storage.UpdateMessage(ctx, userMsg); err != nil {
		return err
	}

	// Update assistant message token count
	assistantMsg.TokenCount = assistantTokens
	if err := r.storage.UpdateMessage(ctx, assistantMsg); err != nil {
		return err
	}

	// Update topic token count
	if t != nil {
		if err := r.storage.UpdateTopicTokenCount(ctx, t.ID); err != nil {
			return err
		}
	}

	// Update conversation token count
	return r.storage.UpdateConversationTokenCount(ctx, conv.ID)
}

// UpdateConversation updates a conversation's metadata.
func (r *Nostop) UpdateConversation(ctx context.Context, conv *storage.Conversation) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.storage.UpdateConversation(ctx, conv)
}

// DeleteConversation deletes a conversation and all related data.
func (r *Nostop) DeleteConversation(ctx context.Context, convID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.storage.DeleteConversation(ctx, convID)
}

// GetMessages returns all messages for a conversation.
func (r *Nostop) GetMessages(ctx context.Context, convID string) ([]storage.Message, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.storage.ListMessages(ctx, convID)
}

// GetActiveMessages returns only active (non-archived) messages.
func (r *Nostop) GetActiveMessages(ctx context.Context, convID string) ([]storage.Message, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.storage.ListActiveMessages(ctx, convID)
}

// GetArchiveStats returns archival statistics for a conversation.
func (r *Nostop) GetArchiveStats(ctx context.Context, convID string) (*ArchiveStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.archiver.GetArchiveStats(ctx, convID, r.config.MaxContextTokens)
}

// FindTopicsToRestore finds archived topics that may be relevant to a query.
func (r *Nostop) FindTopicsToRestore(ctx context.Context, convID, query string) ([]storage.Topic, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.archiver.FindTopicsToRestore(ctx, convID, query)
}

// ScoreTopicRelevance scores a topic's relevance to a query.
func (r *Nostop) ScoreTopicRelevance(ctx context.Context, topicID, query string) (float64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, err := r.storage.GetTopic(ctx, topicID)
	if err != nil {
		return 0, err
	}
	if t == nil {
		return 0, ErrTopicNotFound
	}

	return r.scorer.ScoreRelevance(ctx, topic.Topic{
		ID:       t.ID,
		Name:     t.Name,
		Keywords: t.Keywords,
	}, query)
}

// ExportConversation exports a conversation as JSON.
func (r *Nostop) ExportConversation(ctx context.Context, convID string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conv, err := r.storage.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, fmt.Errorf("conversation not found: %s", convID)
	}

	topics, err := r.storage.ListTopics(ctx, convID)
	if err != nil {
		return nil, err
	}

	messages, err := r.storage.ListMessages(ctx, convID)
	if err != nil {
		return nil, err
	}

	export := struct {
		Conversation *storage.Conversation `json:"conversation"`
		Topics       []storage.Topic       `json:"topics"`
		Messages     []storage.Message     `json:"messages"`
	}{
		Conversation: conv,
		Topics:       topics,
		Messages:     messages,
	}

	return json.MarshalIndent(export, "", "  ")
}
