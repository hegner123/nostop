package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// SQLite provides SQLite storage operations for the RLM system.
type SQLite struct {
	db *sql.DB
}

// NewSQLite creates a new SQLite storage instance.
func NewSQLite(dbPath string) (*SQLite, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign keys and WAL mode for better performance
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set pragma: %w", err)
		}
	}

	return &SQLite{db: db}, nil
}

// Close closes the database connection.
func (s *SQLite) Close() error {
	return s.db.Close()
}

// DB returns the underlying database connection.
func (s *SQLite) DB() *sql.DB {
	return s.db
}

// InitSchema creates the database schema.
func (s *SQLite) InitSchema() error {
	_, err := s.db.Exec(Schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------------------
// Conversation Operations
// ----------------------------------------------------------------------------

// CreateConversation creates a new conversation.
func (s *SQLite) CreateConversation(ctx context.Context, conv *Conversation) error {
	if conv.ID == "" {
		conv.ID = uuid.New().String()
	}
	now := time.Now()
	conv.CreatedAt = now
	conv.UpdatedAt = now

	query := `
		INSERT INTO conversations (id, title, model, system_prompt, total_token_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query,
		conv.ID, conv.Title, conv.Model, conv.SystemPrompt, conv.TotalTokenCount,
		conv.CreatedAt, conv.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create conversation: %w", err)
	}
	return nil
}

// GetConversation retrieves a conversation by ID.
func (s *SQLite) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	query := `
		SELECT id, title, model, system_prompt, total_token_count, created_at, updated_at
		FROM conversations WHERE id = ?
	`
	var conv Conversation
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&conv.ID, &conv.Title, &conv.Model, &conv.SystemPrompt, &conv.TotalTokenCount,
		&conv.CreatedAt, &conv.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	return &conv, nil
}

// UpdateConversation updates a conversation.
func (s *SQLite) UpdateConversation(ctx context.Context, conv *Conversation) error {
	conv.UpdatedAt = time.Now()

	query := `
		UPDATE conversations
		SET title = ?, model = ?, system_prompt = ?, total_token_count = ?, updated_at = ?
		WHERE id = ?
	`
	result, err := s.db.ExecContext(ctx, query,
		conv.Title, conv.Model, conv.SystemPrompt, conv.TotalTokenCount, conv.UpdatedAt,
		conv.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update conversation: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return errors.New("conversation not found")
	}
	return nil
}

// DeleteConversation deletes a conversation and all related data.
func (s *SQLite) DeleteConversation(ctx context.Context, id string) error {
	query := "DELETE FROM conversations WHERE id = ?"
	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete conversation: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return errors.New("conversation not found")
	}
	return nil
}

// ListConversations retrieves all conversations ordered by updated_at descending.
func (s *SQLite) ListConversations(ctx context.Context, limit, offset int) ([]Conversation, error) {
	query := `
		SELECT id, title, model, system_prompt, total_token_count, created_at, updated_at
		FROM conversations
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	defer rows.Close()

	var conversations []Conversation
	for rows.Next() {
		var conv Conversation
		err := rows.Scan(
			&conv.ID, &conv.Title, &conv.Model, &conv.SystemPrompt, &conv.TotalTokenCount,
			&conv.CreatedAt, &conv.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan conversation: %w", err)
		}
		conversations = append(conversations, conv)
	}
	return conversations, rows.Err()
}

// ----------------------------------------------------------------------------
// Topic Operations
// ----------------------------------------------------------------------------

// CreateTopic creates a new topic.
func (s *SQLite) CreateTopic(ctx context.Context, topic *Topic) error {
	if topic.ID == "" {
		topic.ID = uuid.New().String()
	}
	now := time.Now()
	topic.CreatedAt = now
	topic.UpdatedAt = now

	query := `
		INSERT INTO topics (id, conversation_id, name, keywords, token_count, relevance_score, is_current, archived_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query,
		topic.ID, topic.ConversationID, topic.Name, topic.KeywordsJSON(),
		topic.TokenCount, topic.RelevanceScore, topic.IsCurrent, topic.ArchivedAt,
		topic.CreatedAt, topic.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create topic: %w", err)
	}
	return nil
}

// GetTopic retrieves a topic by ID.
func (s *SQLite) GetTopic(ctx context.Context, id string) (*Topic, error) {
	query := `
		SELECT id, conversation_id, name, keywords, token_count, relevance_score, is_current, archived_at, created_at, updated_at
		FROM topics WHERE id = ?
	`
	var topic Topic
	var keywordsJSON string
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&topic.ID, &topic.ConversationID, &topic.Name, &keywordsJSON,
		&topic.TokenCount, &topic.RelevanceScore, &topic.IsCurrent, &topic.ArchivedAt,
		&topic.CreatedAt, &topic.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get topic: %w", err)
	}

	if err := topic.SetKeywordsFromJSON(keywordsJSON); err != nil {
		return nil, fmt.Errorf("failed to parse keywords: %w", err)
	}
	return &topic, nil
}

// UpdateTopic updates a topic.
func (s *SQLite) UpdateTopic(ctx context.Context, topic *Topic) error {
	topic.UpdatedAt = time.Now()

	query := `
		UPDATE topics
		SET name = ?, keywords = ?, token_count = ?, relevance_score = ?, is_current = ?, archived_at = ?, updated_at = ?
		WHERE id = ?
	`
	result, err := s.db.ExecContext(ctx, query,
		topic.Name, topic.KeywordsJSON(), topic.TokenCount, topic.RelevanceScore,
		topic.IsCurrent, topic.ArchivedAt, topic.UpdatedAt,
		topic.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update topic: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return errors.New("topic not found")
	}
	return nil
}

// DeleteTopic deletes a topic.
func (s *SQLite) DeleteTopic(ctx context.Context, id string) error {
	query := "DELETE FROM topics WHERE id = ?"
	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete topic: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return errors.New("topic not found")
	}
	return nil
}

// ListTopics retrieves all topics for a conversation.
func (s *SQLite) ListTopics(ctx context.Context, conversationID string) ([]Topic, error) {
	query := `
		SELECT id, conversation_id, name, keywords, token_count, relevance_score, is_current, archived_at, created_at, updated_at
		FROM topics
		WHERE conversation_id = ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to list topics: %w", err)
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var topic Topic
		var keywordsJSON string
		err := rows.Scan(
			&topic.ID, &topic.ConversationID, &topic.Name, &keywordsJSON,
			&topic.TokenCount, &topic.RelevanceScore, &topic.IsCurrent, &topic.ArchivedAt,
			&topic.CreatedAt, &topic.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan topic: %w", err)
		}
		if err := topic.SetKeywordsFromJSON(keywordsJSON); err != nil {
			return nil, fmt.Errorf("failed to parse keywords: %w", err)
		}
		topics = append(topics, topic)
	}
	return topics, rows.Err()
}

// GetCurrentTopic retrieves the current topic for a conversation.
func (s *SQLite) GetCurrentTopic(ctx context.Context, conversationID string) (*Topic, error) {
	query := `
		SELECT id, conversation_id, name, keywords, token_count, relevance_score, is_current, archived_at, created_at, updated_at
		FROM topics
		WHERE conversation_id = ? AND is_current = TRUE
		LIMIT 1
	`
	var topic Topic
	var keywordsJSON string
	err := s.db.QueryRowContext(ctx, query, conversationID).Scan(
		&topic.ID, &topic.ConversationID, &topic.Name, &keywordsJSON,
		&topic.TokenCount, &topic.RelevanceScore, &topic.IsCurrent, &topic.ArchivedAt,
		&topic.CreatedAt, &topic.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get current topic: %w", err)
	}

	if err := topic.SetKeywordsFromJSON(keywordsJSON); err != nil {
		return nil, fmt.Errorf("failed to parse keywords: %w", err)
	}
	return &topic, nil
}

// SetCurrentTopic sets a topic as current and clears the current flag on others.
func (s *SQLite) SetCurrentTopic(ctx context.Context, conversationID, topicID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Clear current flag on all topics in conversation
	_, err = tx.ExecContext(ctx,
		"UPDATE topics SET is_current = FALSE, updated_at = ? WHERE conversation_id = ?",
		time.Now(), conversationID,
	)
	if err != nil {
		return fmt.Errorf("failed to clear current topics: %w", err)
	}

	// Set current flag on specified topic
	result, err := tx.ExecContext(ctx,
		"UPDATE topics SET is_current = TRUE, updated_at = ? WHERE id = ?",
		time.Now(), topicID,
	)
	if err != nil {
		return fmt.Errorf("failed to set current topic: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return errors.New("topic not found")
	}

	return tx.Commit()
}

// GetActiveTopics retrieves all non-archived topics for a conversation.
func (s *SQLite) GetActiveTopics(ctx context.Context, conversationID string) ([]Topic, error) {
	query := `
		SELECT id, conversation_id, name, keywords, token_count, relevance_score, is_current, archived_at, created_at, updated_at
		FROM topics
		WHERE conversation_id = ? AND archived_at IS NULL
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active topics: %w", err)
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var topic Topic
		var keywordsJSON string
		err := rows.Scan(
			&topic.ID, &topic.ConversationID, &topic.Name, &keywordsJSON,
			&topic.TokenCount, &topic.RelevanceScore, &topic.IsCurrent, &topic.ArchivedAt,
			&topic.CreatedAt, &topic.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan topic: %w", err)
		}
		if err := topic.SetKeywordsFromJSON(keywordsJSON); err != nil {
			return nil, fmt.Errorf("failed to parse keywords: %w", err)
		}
		topics = append(topics, topic)
	}
	return topics, rows.Err()
}

// ----------------------------------------------------------------------------
// Message Operations
// ----------------------------------------------------------------------------

// CreateMessage creates a new message.
func (s *SQLite) CreateMessage(ctx context.Context, msg *Message) error {
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	msg.CreatedAt = time.Now()

	query := `
		INSERT INTO messages (id, conversation_id, topic_id, role, content, token_count, is_archived, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query,
		msg.ID, msg.ConversationID, msg.TopicID, msg.Role, msg.Content,
		msg.TokenCount, msg.IsArchived, msg.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}
	return nil
}

// GetMessage retrieves a message by ID.
func (s *SQLite) GetMessage(ctx context.Context, id string) (*Message, error) {
	query := `
		SELECT id, conversation_id, topic_id, role, content, token_count, is_archived, created_at
		FROM messages WHERE id = ?
	`
	var msg Message
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&msg.ID, &msg.ConversationID, &msg.TopicID, &msg.Role, &msg.Content,
		&msg.TokenCount, &msg.IsArchived, &msg.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	return &msg, nil
}

// UpdateMessage updates a message.
func (s *SQLite) UpdateMessage(ctx context.Context, msg *Message) error {
	query := `
		UPDATE messages
		SET topic_id = ?, content = ?, token_count = ?, is_archived = ?
		WHERE id = ?
	`
	result, err := s.db.ExecContext(ctx, query,
		msg.TopicID, msg.Content, msg.TokenCount, msg.IsArchived,
		msg.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update message: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return errors.New("message not found")
	}
	return nil
}

// DeleteMessage deletes a message.
func (s *SQLite) DeleteMessage(ctx context.Context, id string) error {
	query := "DELETE FROM messages WHERE id = ?"
	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return errors.New("message not found")
	}
	return nil
}

// ListMessages retrieves all messages for a conversation.
func (s *SQLite) ListMessages(ctx context.Context, conversationID string) ([]Message, error) {
	query := `
		SELECT id, conversation_id, topic_id, role, content, token_count, is_archived, created_at
		FROM messages
		WHERE conversation_id = ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		err := rows.Scan(
			&msg.ID, &msg.ConversationID, &msg.TopicID, &msg.Role, &msg.Content,
			&msg.TokenCount, &msg.IsArchived, &msg.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

// ListActiveMessages retrieves all non-archived messages for a conversation.
func (s *SQLite) ListActiveMessages(ctx context.Context, conversationID string) ([]Message, error) {
	query := `
		SELECT id, conversation_id, topic_id, role, content, token_count, is_archived, created_at
		FROM messages
		WHERE conversation_id = ? AND is_archived = FALSE
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		err := rows.Scan(
			&msg.ID, &msg.ConversationID, &msg.TopicID, &msg.Role, &msg.Content,
			&msg.TokenCount, &msg.IsArchived, &msg.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

// ListMessagesByTopic retrieves all messages for a specific topic.
func (s *SQLite) ListMessagesByTopic(ctx context.Context, topicID string) ([]Message, error) {
	query := `
		SELECT id, conversation_id, topic_id, role, content, token_count, is_archived, created_at
		FROM messages
		WHERE topic_id = ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, topicID)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages by topic: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		err := rows.Scan(
			&msg.ID, &msg.ConversationID, &msg.TopicID, &msg.Role, &msg.Content,
			&msg.TokenCount, &msg.IsArchived, &msg.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

// ----------------------------------------------------------------------------
// Archive Operations
// ----------------------------------------------------------------------------

// ArchiveTopic archives a topic and its messages.
func (s *SQLite) ArchiveTopic(ctx context.Context, topicID string, usageBefore, usageAfter float64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()

	// Get topic info
	var topic Topic
	var keywordsJSON string
	err = tx.QueryRowContext(ctx,
		"SELECT id, conversation_id, name, keywords, token_count, relevance_score, is_current, archived_at, created_at, updated_at FROM topics WHERE id = ?",
		topicID,
	).Scan(&topic.ID, &topic.ConversationID, &topic.Name, &keywordsJSON,
		&topic.TokenCount, &topic.RelevanceScore, &topic.IsCurrent, &topic.ArchivedAt,
		&topic.CreatedAt, &topic.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to get topic: %w", err)
	}

	// Get messages for this topic
	rows, err := tx.QueryContext(ctx,
		"SELECT id, content, token_count FROM messages WHERE topic_id = ? AND is_archived = FALSE",
		topicID,
	)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}

	var tokensAffected int
	var messageIDs []string
	var archiveInserts []struct {
		id        string
		messageID string
		content   string
	}

	for rows.Next() {
		var msgID, content string
		var tokenCount int
		if err := rows.Scan(&msgID, &content, &tokenCount); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan message: %w", err)
		}
		tokensAffected += tokenCount
		messageIDs = append(messageIDs, msgID)
		archiveInserts = append(archiveInserts, struct {
			id        string
			messageID string
			content   string
		}{
			id:        uuid.New().String(),
			messageID: msgID,
			content:   content,
		})
	}
	rows.Close()

	// Archive messages to message_archive table
	for _, ins := range archiveInserts {
		_, err = tx.ExecContext(ctx,
			"INSERT INTO message_archive (id, message_id, topic_id, full_content, archived_at) VALUES (?, ?, ?, ?, ?)",
			ins.id, ins.messageID, topicID, ins.content, now,
		)
		if err != nil {
			return fmt.Errorf("failed to archive message: %w", err)
		}
	}

	// Mark messages as archived
	for _, msgID := range messageIDs {
		_, err = tx.ExecContext(ctx,
			"UPDATE messages SET is_archived = TRUE WHERE id = ?",
			msgID,
		)
		if err != nil {
			return fmt.Errorf("failed to mark message as archived: %w", err)
		}
	}

	// Mark topic as archived
	_, err = tx.ExecContext(ctx,
		"UPDATE topics SET archived_at = ?, is_current = FALSE, updated_at = ? WHERE id = ?",
		now, now, topicID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark topic as archived: %w", err)
	}

	// Record archive event
	_, err = tx.ExecContext(ctx,
		"INSERT INTO archive_events (conversation_id, topic_id, action, tokens_affected, context_usage_before, context_usage_after, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		topic.ConversationID, topicID, ArchiveActionArchive, tokensAffected, usageBefore, usageAfter, now,
	)
	if err != nil {
		return fmt.Errorf("failed to record archive event: %w", err)
	}

	return tx.Commit()
}

// RestoreTopic restores an archived topic and its messages.
func (s *SQLite) RestoreTopic(ctx context.Context, topicID string, usageBefore, usageAfter float64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()

	// Get topic info
	var topic Topic
	var keywordsJSON string
	err = tx.QueryRowContext(ctx,
		"SELECT id, conversation_id, name, keywords, token_count, relevance_score, is_current, archived_at, created_at, updated_at FROM topics WHERE id = ?",
		topicID,
	).Scan(&topic.ID, &topic.ConversationID, &topic.Name, &keywordsJSON,
		&topic.TokenCount, &topic.RelevanceScore, &topic.IsCurrent, &topic.ArchivedAt,
		&topic.CreatedAt, &topic.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to get topic: %w", err)
	}

	if topic.ArchivedAt == nil {
		return errors.New("topic is not archived")
	}

	// Get archived messages
	rows, err := tx.QueryContext(ctx,
		"SELECT message_id, full_content FROM message_archive WHERE topic_id = ?",
		topicID,
	)
	if err != nil {
		return fmt.Errorf("failed to get archived messages: %w", err)
	}

	var tokensAffected int
	type restore struct {
		messageID string
		content   string
	}
	var restores []restore

	for rows.Next() {
		var r restore
		if err := rows.Scan(&r.messageID, &r.content); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan archived message: %w", err)
		}
		restores = append(restores, r)
	}
	rows.Close()

	// Restore messages
	for _, r := range restores {
		// Get token count for the message
		var tokenCount int
		err = tx.QueryRowContext(ctx, "SELECT token_count FROM messages WHERE id = ?", r.messageID).Scan(&tokenCount)
		if err != nil {
			return fmt.Errorf("failed to get message token count: %w", err)
		}
		tokensAffected += tokenCount

		// Update message content and unarchive
		_, err = tx.ExecContext(ctx,
			"UPDATE messages SET content = ?, is_archived = FALSE WHERE id = ?",
			r.content, r.messageID,
		)
		if err != nil {
			return fmt.Errorf("failed to restore message: %w", err)
		}
	}

	// Delete archive records
	_, err = tx.ExecContext(ctx, "DELETE FROM message_archive WHERE topic_id = ?", topicID)
	if err != nil {
		return fmt.Errorf("failed to delete archive records: %w", err)
	}

	// Unarchive topic
	_, err = tx.ExecContext(ctx,
		"UPDATE topics SET archived_at = NULL, updated_at = ? WHERE id = ?",
		now, topicID,
	)
	if err != nil {
		return fmt.Errorf("failed to unarchive topic: %w", err)
	}

	// Record restore event
	_, err = tx.ExecContext(ctx,
		"INSERT INTO archive_events (conversation_id, topic_id, action, tokens_affected, context_usage_before, context_usage_after, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		topic.ConversationID, topicID, ArchiveActionRestore, tokensAffected, usageBefore, usageAfter, now,
	)
	if err != nil {
		return fmt.Errorf("failed to record restore event: %w", err)
	}

	return tx.Commit()
}

// GetArchivedContent retrieves the archived content for a message.
func (s *SQLite) GetArchivedContent(ctx context.Context, messageID string) (*MessageArchive, error) {
	query := `
		SELECT id, message_id, topic_id, full_content, archived_at
		FROM message_archive WHERE message_id = ?
	`
	var archive MessageArchive
	err := s.db.QueryRowContext(ctx, query, messageID).Scan(
		&archive.ID, &archive.MessageID, &archive.TopicID, &archive.FullContent, &archive.ArchivedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get archived content: %w", err)
	}
	return &archive, nil
}

// ListArchiveEvents retrieves archive events for a conversation.
func (s *SQLite) ListArchiveEvents(ctx context.Context, conversationID string, limit int) ([]ArchiveEvent, error) {
	query := `
		SELECT id, conversation_id, topic_id, action, tokens_affected, context_usage_before, context_usage_after, created_at
		FROM archive_events
		WHERE conversation_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list archive events: %w", err)
	}
	defer rows.Close()

	var events []ArchiveEvent
	for rows.Next() {
		var event ArchiveEvent
		err := rows.Scan(
			&event.ID, &event.ConversationID, &event.TopicID, &event.Action,
			&event.TokensAffected, &event.ContextUsageBefore, &event.ContextUsageAfter, &event.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan archive event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// ----------------------------------------------------------------------------
// Statistics
// ----------------------------------------------------------------------------

// GetConversationStats retrieves statistics for a conversation.
func (s *SQLite) GetConversationStats(ctx context.Context, conversationID string, maxContextTokens int) (*ConversationStats, error) {
	stats := &ConversationStats{}

	// Get message count and total tokens
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*), COALESCE(SUM(token_count), 0) FROM messages WHERE conversation_id = ?",
		conversationID,
	).Scan(&stats.MessageCount, &stats.TotalTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to get message stats: %w", err)
	}

	// Get archived tokens
	err = s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(token_count), 0) FROM messages WHERE conversation_id = ? AND is_archived = TRUE",
		conversationID,
	).Scan(&stats.ArchivedTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to get archived token stats: %w", err)
	}

	stats.ActiveTokens = stats.TotalTokens - stats.ArchivedTokens

	// Get topic counts
	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM topics WHERE conversation_id = ?",
		conversationID,
	).Scan(&stats.TopicCount)
	if err != nil {
		return nil, fmt.Errorf("failed to get topic count: %w", err)
	}

	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM topics WHERE conversation_id = ? AND archived_at IS NOT NULL",
		conversationID,
	).Scan(&stats.ArchivedTopicCount)
	if err != nil {
		return nil, fmt.Errorf("failed to get archived topic count: %w", err)
	}

	// Calculate usage percent
	if maxContextTokens > 0 {
		stats.ContextUsagePercent = float64(stats.ActiveTokens) / float64(maxContextTokens)
	}

	return stats, nil
}

// GetTopicsForArchival returns topics sorted by priority for archival (lowest score first).
func (s *SQLite) GetTopicsForArchival(ctx context.Context, conversationID string) ([]TopicScore, error) {
	query := `
		SELECT
			t.id, t.name, t.token_count, t.relevance_score,
			CASE
				WHEN MAX(m.created_at) IS NULL THEN 0
				ELSE (julianday('now') - julianday(MAX(m.created_at))) * 24
			END as hours_since_last_message
		FROM topics t
		LEFT JOIN messages m ON m.topic_id = t.id AND m.is_archived = FALSE
		WHERE t.conversation_id = ? AND t.archived_at IS NULL
		GROUP BY t.id
		ORDER BY t.relevance_score ASC, hours_since_last_message DESC
	`
	rows, err := s.db.QueryContext(ctx, query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get topics for archival: %w", err)
	}
	defer rows.Close()

	var scores []TopicScore
	for rows.Next() {
		var score TopicScore
		var hoursSinceLastMessage float64
		err := rows.Scan(&score.TopicID, &score.Name, &score.TokenCount, &score.RelevanceScore, &hoursSinceLastMessage)
		if err != nil {
			return nil, fmt.Errorf("failed to scan topic score: %w", err)
		}
		// Calculate recency score: 1.0 - (hours/24), min 0.1
		score.RecencyScore = 1.0 - (hoursSinceLastMessage / 24.0)
		if score.RecencyScore < 0.1 {
			score.RecencyScore = 0.1
		}
		// Combined score (lower = archive first)
		score.CombinedScore = score.RelevanceScore * score.RecencyScore
		scores = append(scores, score)
	}
	return scores, rows.Err()
}

// UpdateTopicTokenCount recalculates and updates the token count for a topic.
func (s *SQLite) UpdateTopicTokenCount(ctx context.Context, topicID string) error {
	query := `
		UPDATE topics
		SET token_count = (
			SELECT COALESCE(SUM(token_count), 0)
			FROM messages
			WHERE topic_id = ? AND is_archived = FALSE
		),
		updated_at = ?
		WHERE id = ?
	`
	_, err := s.db.ExecContext(ctx, query, topicID, time.Now(), topicID)
	if err != nil {
		return fmt.Errorf("failed to update topic token count: %w", err)
	}
	return nil
}

// UpdateConversationTokenCount recalculates and updates the total token count for a conversation.
func (s *SQLite) UpdateConversationTokenCount(ctx context.Context, conversationID string) error {
	query := `
		UPDATE conversations
		SET total_token_count = (
			SELECT COALESCE(SUM(token_count), 0)
			FROM messages
			WHERE conversation_id = ?
		),
		updated_at = ?
		WHERE id = ?
	`
	_, err := s.db.ExecContext(ctx, query, conversationID, time.Now(), conversationID)
	if err != nil {
		return fmt.Errorf("failed to update conversation token count: %w", err)
	}
	return nil
}
