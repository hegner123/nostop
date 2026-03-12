// Package rlm provides the main RLM engine for intelligent topic-based context archival.
package rlm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/user/rlm/internal/api"
	"github.com/user/rlm/internal/storage"
	"github.com/user/rlm/internal/topic"
)

// Config contains configuration for the RLM engine.
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
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxContextTokens: 200000,
		ArchiveThreshold: ThresholdArchive,
		ArchiveTarget:    ArchiveTarget,
		DetectionModel:   api.ModelHaiku35Latest,
		ChatModel:        api.ModelSonnet45Latest,
	}
}

// RLM is the main orchestrator for the topic-based context archival system.
type RLM struct {
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

	mu sync.RWMutex
	wg sync.WaitGroup // tracks background goroutines (e.g., relevance re-scoring)
}

// Response is the result of sending a message through the RLM engine.
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

// New creates a new RLM engine instance with all components initialized.
func New(cfg Config) (*RLM, error) {
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
		cfg.DetectionModel = api.ModelHaiku35Latest
	}
	if cfg.ChatModel == "" {
		cfg.ChatModel = api.ModelSonnet45Latest
	}

	// Initialize API client
	client := api.NewClient(cfg.APIKey)

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

	return &RLM{
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
	}, nil
}

// Send sends a user message and returns the assistant's response.
// This is the main method that orchestrates all RLM functionality:
// 1. Add user message to current topic
// 2. Check for topic shift (async via haiku)
// 3. Calculate context usage
// 4. If >= 95%, archive until 50% free
// 5. Build context from active topics
// 6. Send to Claude, get response
// 7. Store assistant response
func (r *RLM) Send(ctx context.Context, convID, message string) (*Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get or verify conversation exists
	conv, err := r.storage.GetConversation(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	if conv == nil {
		return nil, fmt.Errorf("conversation not found: %s", convID)
	}

	// Load topics for this conversation
	if err := r.tracker.LoadTopics(ctx, convID); err != nil {
		return nil, fmt.Errorf("failed to load topics: %w", err)
	}

	// Get or create current topic
	currentTopic := r.tracker.GetCurrentTopic()
	if currentTopic == nil {
		// Create initial topic for this conversation
		currentTopic, err = r.createInitialTopic(ctx, conv, message)
		if err != nil {
			return nil, fmt.Errorf("failed to create initial topic: %w", err)
		}
	}

	// Create and store user message
	userMsg, err := r.createMessage(ctx, conv, currentTopic, storage.RoleUser, message)
	if err != nil {
		return nil, fmt.Errorf("failed to store user message: %w", err)
	}

	// Check for topic shift asynchronously (using recent messages)
	topicShift, err := r.detectTopicShift(ctx, convID, currentTopic)
	if err != nil {
		// Log but don't fail - topic detection is not critical
		topicShift = nil
	}

	// Handle topic shift if detected
	if topicShift != nil && topicShift.Detected {
		newTopic, err := r.handleTopicShift(ctx, conv, topicShift, userMsg)
		if err != nil {
			// Log but continue with current topic
			topicShift = nil
		} else {
			currentTopic = newTopic
		}
	}

	// Calculate context usage
	usage, err := r.context.GetUsage(ctx, conv)
	if err != nil {
		return nil, fmt.Errorf("failed to get context usage: %w", err)
	}

	// Archive topics if needed
	var archived []storage.Topic
	if r.context.ShouldArchive(usage) {
		archived, err = r.archiver.ArchiveUntilTarget(
			ctx,
			convID,
			usage.UsagePercent,
			r.config.MaxContextTokens,
			r.config.ArchiveTarget,
		)
		if err != nil && !errors.Is(err, ErrNoTopicsToArchive) {
			return nil, fmt.Errorf("failed to archive topics: %w", err)
		}
	}

	// Build context from active topics (includes the user message stored above)
	messages, err := r.BuildContext(ctx, conv)
	if err != nil {
		return nil, fmt.Errorf("failed to build context: %w", err)
	}

	// Send to Claude
	req := &api.Request{
		Model:     r.config.ChatModel,
		MaxTokens: 8192,
		Messages:  messages,
	}

	// Add system prompt with cache control if present
	if conv.SystemPrompt != "" {
		req.System = r.cacheStrategy.ApplyToSystemPrompt(conv.SystemPrompt)
	}

	resp, err := r.client.Send(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Track cache statistics
	requestCacheStats := NewCacheStats()
	requestCacheStats.Update(resp.Usage)
	r.cacheStats.Update(resp.Usage)

	// Extract response text
	responseText := resp.GetText()

	// Store assistant response
	assistantMsg, err := r.createMessage(ctx, conv, currentTopic, storage.RoleAssistant, responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to store assistant message: %w", err)
	}

	// Update token counts
	if err := r.updateTokenCounts(ctx, conv, currentTopic, userMsg, assistantMsg, resp.Usage); err != nil {
		// Log but don't fail
	}

	return &Response{
		Content:    responseText,
		TokensUsed: resp.Usage.InputTokens + resp.Usage.OutputTokens,
		TopicShift: topicShift,
		Archived:   archived,
		Usage:      resp.Usage,
		MessageID:  assistantMsg.ID,
		CacheStats: requestCacheStats,
	}, nil
}

// SendStream sends a user message and streams the response via callback.
func (r *RLM) SendStream(ctx context.Context, convID, message string, callback api.StreamCallback) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get or verify conversation exists
	conv, err := r.storage.GetConversation(ctx, convID)
	if err != nil {
		return fmt.Errorf("failed to get conversation: %w", err)
	}
	if conv == nil {
		return fmt.Errorf("conversation not found: %s", convID)
	}

	// Load topics for this conversation
	if err := r.tracker.LoadTopics(ctx, convID); err != nil {
		return fmt.Errorf("failed to load topics: %w", err)
	}

	// Get or create current topic
	currentTopic := r.tracker.GetCurrentTopic()
	if currentTopic == nil {
		currentTopic, err = r.createInitialTopic(ctx, conv, message)
		if err != nil {
			return fmt.Errorf("failed to create initial topic: %w", err)
		}
	}

	// Create and store user message
	userMsg, err := r.createMessage(ctx, conv, currentTopic, storage.RoleUser, message)
	if err != nil {
		return fmt.Errorf("failed to store user message: %w", err)
	}

	// Check for topic shift
	topicShift, _ := r.detectTopicShift(ctx, convID, currentTopic)
	if topicShift != nil && topicShift.Detected {
		newTopic, err := r.handleTopicShift(ctx, conv, topicShift, userMsg)
		if err == nil {
			currentTopic = newTopic
		}
	}

	// Calculate context usage and archive if needed
	usage, err := r.context.GetUsage(ctx, conv)
	if err != nil {
		return fmt.Errorf("failed to get context usage: %w", err)
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
			return fmt.Errorf("failed to archive topics: %w", err)
		}
	}

	// Build context (includes the user message stored above)
	messages, err := r.BuildContext(ctx, conv)
	if err != nil {
		return fmt.Errorf("failed to build context: %w", err)
	}

	// Prepare request
	req := &api.Request{
		Model:     r.config.ChatModel,
		MaxTokens: 8192,
		Messages:  messages,
	}

	// Add system prompt with cache control if present
	if conv.SystemPrompt != "" {
		req.System = r.cacheStrategy.ApplyToSystemPrompt(conv.SystemPrompt)
	}

	// Send streaming request
	resp, err := r.client.StreamWithCallback(ctx, req, callback)
	if err != nil {
		return fmt.Errorf("failed to stream: %w", err)
	}

	// Track cache statistics
	r.cacheStats.Update(resp.Usage)

	// Store assistant response
	responseText := resp.GetText()
	assistantMsg, err := r.createMessage(ctx, conv, currentTopic, storage.RoleAssistant, responseText)
	if err != nil {
		return fmt.Errorf("failed to store assistant message: %w", err)
	}

	// Update token counts
	_ = r.updateTokenCounts(ctx, conv, currentTopic, userMsg, assistantMsg, resp.Usage)

	return nil
}

// BuildContext constructs the message context from active (non-archived) topics.
// It respects topic allocation percentages and includes the system prompt.
func (r *RLM) BuildContext(ctx context.Context, conv *storage.Conversation) ([]api.MessageParam, error) {
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
func (r *RLM) NewConversation(ctx context.Context, title, systemPrompt string) (*Conversation, error) {
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
func (r *RLM) GetConversation(ctx context.Context, convID string) (*Conversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conv, err := r.storage.GetConversation(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	return conv, nil
}

// ListConversations retrieves all conversations.
func (r *RLM) ListConversations(ctx context.Context) ([]Conversation, error) {
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
func (r *RLM) ListConversationsPaginated(ctx context.Context, limit, offset int) ([]Conversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	convs, err := r.storage.ListConversations(ctx, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	return convs, nil
}

// GetContextUsage returns the current context usage for a conversation.
func (r *RLM) GetContextUsage(ctx context.Context, convID string) (*ContextUsage, error) {
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
func (r *RLM) GetTopics(ctx context.Context, convID string) ([]storage.Topic, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.storage.ListTopics(ctx, convID)
}

// GetActiveTopics returns only active (non-archived) topics.
func (r *RLM) GetActiveTopics(ctx context.Context, convID string) ([]storage.Topic, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.storage.GetActiveTopics(ctx, convID)
}

// GetArchivedTopics returns archived topics for a conversation.
func (r *RLM) GetArchivedTopics(ctx context.Context, convID string) ([]storage.Topic, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.archiver.GetArchivedTopics(ctx, convID)
}

// RestoreTopic restores an archived topic to active context.
func (r *RLM) RestoreTopic(ctx context.Context, topicID string) (*storage.Topic, error) {
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

// Close closes the RLM engine and releases resources.
func (r *RLM) Close() error {
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
func (r *RLM) Client() *api.Client {
	return r.client
}

// Storage returns the underlying storage.
func (r *RLM) Storage() *storage.SQLite {
	return r.storage
}

// Config returns the current configuration.
func (r *RLM) Config() Config {
	return r.config
}

// CacheStrategy returns the current cache strategy.
func (r *RLM) CacheStrategy() CacheStrategy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cacheStrategy
}

// SetCacheStrategy updates the cache strategy.
func (r *RLM) SetCacheStrategy(strategy CacheStrategy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cacheStrategy = strategy
}

// CacheStats returns a copy of the cumulative cache statistics.
func (r *RLM) CacheStats() *CacheStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cacheStats.Clone()
}

// ResetCacheStats resets the cumulative cache statistics.
func (r *RLM) ResetCacheStats() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cacheStats.Reset()
}

// CacheDebugInfo returns debug information about caching for display.
func (r *RLM) CacheDebugInfo() *CacheDebugInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return NewCacheDebugInfo(r.cacheStrategy, r.cacheStats.Clone())
}

// --- Internal helpers ---

// createInitialTopic creates the first topic for a conversation.
func (r *RLM) createInitialTopic(ctx context.Context, conv *storage.Conversation, initialMessage string) (*storage.Topic, error) {
	// Use detector to identify the topic
	msg := topic.Message{
		ID:      uuid.New().String(),
		Role:    "user",
		Content: initialMessage,
	}

	detected, err := r.detector.IdentifyTopic(ctx, []topic.Message{msg})
	if err != nil {
		// Fall back to generic topic
		detected = &topic.Topic{
			Name:     "Initial conversation",
			Keywords: []string{},
		}
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

	return storageTopic, nil
}

// detectTopicShift checks if the conversation has shifted to a new topic.
func (r *RLM) detectTopicShift(ctx context.Context, convID string, currentTopic *storage.Topic) (*topic.TopicShift, error) {
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

	return r.detector.DetectTopicShift(ctx, topicMessages, currentTopicForDetection)
}

// handleTopicShift creates a new topic when a shift is detected.
func (r *RLM) handleTopicShift(ctx context.Context, conv *storage.Conversation, shift *topic.TopicShift, triggerMsg *storage.Message) (*storage.Topic, error) {
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
func (r *RLM) createMessage(ctx context.Context, conv *storage.Conversation, t *storage.Topic, role storage.Role, content string) (*storage.Message, error) {
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
func (r *RLM) updateTokenCounts(ctx context.Context, conv *storage.Conversation, t *storage.Topic, userMsg, assistantMsg *storage.Message, usage api.Usage) error {
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
func (r *RLM) UpdateConversation(ctx context.Context, conv *storage.Conversation) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.storage.UpdateConversation(ctx, conv)
}

// DeleteConversation deletes a conversation and all related data.
func (r *RLM) DeleteConversation(ctx context.Context, convID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.storage.DeleteConversation(ctx, convID)
}

// GetMessages returns all messages for a conversation.
func (r *RLM) GetMessages(ctx context.Context, convID string) ([]storage.Message, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.storage.ListMessages(ctx, convID)
}

// GetActiveMessages returns only active (non-archived) messages.
func (r *RLM) GetActiveMessages(ctx context.Context, convID string) ([]storage.Message, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.storage.ListActiveMessages(ctx, convID)
}

// GetArchiveStats returns archival statistics for a conversation.
func (r *RLM) GetArchiveStats(ctx context.Context, convID string) (*ArchiveStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.archiver.GetArchiveStats(ctx, convID, r.config.MaxContextTokens)
}

// FindTopicsToRestore finds archived topics that may be relevant to a query.
func (r *RLM) FindTopicsToRestore(ctx context.Context, convID, query string) ([]storage.Topic, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.archiver.FindTopicsToRestore(ctx, convID, query)
}

// ScoreTopicRelevance scores a topic's relevance to a query.
func (r *RLM) ScoreTopicRelevance(ctx context.Context, topicID, query string) (float64, error) {
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
func (r *RLM) ExportConversation(ctx context.Context, convID string) ([]byte, error) {
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
