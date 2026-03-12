package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// newTestDB creates an in-memory SQLite instance for testing.
func newTestDB(t *testing.T) *SQLite {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	if err := db.InitSchema(); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// createTestConversation creates a conversation for testing.
func createTestConversation(t *testing.T, db *SQLite) *Conversation {
	t.Helper()
	conv := &Conversation{
		ID:           "conv-1",
		Title:        "Test Conversation",
		Model:        "claude-test",
		SystemPrompt: "You are helpful.",
	}
	if err := db.CreateConversation(context.Background(), conv); err != nil {
		t.Fatalf("failed to create conversation: %v", err)
	}
	return conv
}

func TestConversationCRUD(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Create
	conv := createTestConversation(t, db)

	// Read
	got, err := db.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if got.Title != "Test Conversation" {
		t.Errorf("expected title=%q, got %q", "Test Conversation", got.Title)
	}
	if got.SystemPrompt != "You are helpful." {
		t.Errorf("expected system_prompt=%q, got %q", "You are helpful.", got.SystemPrompt)
	}

	// Update
	got.Title = "Updated Title"
	if err := db.UpdateConversation(ctx, got); err != nil {
		t.Fatalf("UpdateConversation: %v", err)
	}
	got2, err := db.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetConversation after update: %v", err)
	}
	if got2.Title != "Updated Title" {
		t.Errorf("expected updated title=%q, got %q", "Updated Title", got2.Title)
	}

	// List
	convs, err := db.ListConversations(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Errorf("expected 1 conversation, got %d", len(convs))
	}

	// Delete
	if err := db.DeleteConversation(ctx, conv.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	deleted, err := db.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetConversation after delete: %v", err)
	}
	if deleted != nil {
		t.Error("expected nil after delete")
	}
}

func TestGetConversationNotFound(t *testing.T) {
	db := newTestDB(t)
	got, err := db.GetConversation(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent conversation")
	}
}

func TestTopicCRUD(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	conv := createTestConversation(t, db)

	// Create topic
	topic := &Topic{
		ID:             "topic-1",
		ConversationID: conv.ID,
		Name:           "Go programming",
		Keywords:       []string{"go", "golang", "concurrency"},
		RelevanceScore: 0.9,
		IsCurrent:      true,
	}
	if err := db.CreateTopic(ctx, topic); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	// Read
	got, err := db.GetTopic(ctx, "topic-1")
	if err != nil {
		t.Fatalf("GetTopic: %v", err)
	}
	if got.Name != "Go programming" {
		t.Errorf("expected name=%q, got %q", "Go programming", got.Name)
	}
	if len(got.Keywords) != 3 || got.Keywords[0] != "go" {
		t.Errorf("unexpected keywords: %v", got.Keywords)
	}
	if !got.IsCurrent {
		t.Error("expected IsCurrent=true")
	}

	// List topics
	topics, err := db.ListTopics(ctx, conv.ID)
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if len(topics) != 1 {
		t.Errorf("expected 1 topic, got %d", len(topics))
	}

	// Get current topic
	current, err := db.GetCurrentTopic(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetCurrentTopic: %v", err)
	}
	if current == nil || current.ID != "topic-1" {
		t.Error("expected topic-1 as current topic")
	}

	// Update
	got.Name = "Advanced Go"
	got.RelevanceScore = 0.5
	if err := db.UpdateTopic(ctx, got); err != nil {
		t.Fatalf("UpdateTopic: %v", err)
	}
	got2, err := db.GetTopic(ctx, "topic-1")
	if err != nil {
		t.Fatalf("GetTopic after update: %v", err)
	}
	if got2.Name != "Advanced Go" {
		t.Errorf("expected name=%q, got %q", "Advanced Go", got2.Name)
	}

	// Delete
	if err := db.DeleteTopic(ctx, "topic-1"); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	deleted, err := db.GetTopic(ctx, "topic-1")
	if err != nil {
		t.Fatalf("GetTopic after delete: %v", err)
	}
	if deleted != nil {
		t.Error("expected nil after delete")
	}
}

func TestSetCurrentTopic(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	conv := createTestConversation(t, db)

	// Create two topics
	t1 := &Topic{ID: "t1", ConversationID: conv.ID, Name: "Topic 1", Keywords: []string{}, IsCurrent: true}
	t2 := &Topic{ID: "t2", ConversationID: conv.ID, Name: "Topic 2", Keywords: []string{}, IsCurrent: false}
	if err := db.CreateTopic(ctx, t1); err != nil {
		t.Fatalf("CreateTopic t1: %v", err)
	}
	if err := db.CreateTopic(ctx, t2); err != nil {
		t.Fatalf("CreateTopic t2: %v", err)
	}

	// Switch current to t2
	if err := db.SetCurrentTopic(ctx, conv.ID, "t2"); err != nil {
		t.Fatalf("SetCurrentTopic: %v", err)
	}

	// Verify t1 is no longer current
	got1, err := db.GetTopic(ctx, "t1")
	if err != nil {
		t.Fatalf("GetTopic t1: %v", err)
	}
	if got1.IsCurrent {
		t.Error("expected t1.IsCurrent=false after switch")
	}

	// Verify t2 is now current
	got2, err := db.GetTopic(ctx, "t2")
	if err != nil {
		t.Fatalf("GetTopic t2: %v", err)
	}
	if !got2.IsCurrent {
		t.Error("expected t2.IsCurrent=true after switch")
	}

	// GetCurrentTopic should return t2
	current, err := db.GetCurrentTopic(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetCurrentTopic: %v", err)
	}
	if current == nil || current.ID != "t2" {
		t.Error("expected t2 as current topic")
	}
}

func TestMessageCRUD(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	conv := createTestConversation(t, db)

	topicID := "topic-msg"
	topic := &Topic{ID: topicID, ConversationID: conv.ID, Name: "Test", Keywords: []string{}}
	if err := db.CreateTopic(ctx, topic); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	// Create messages
	msg1 := &Message{
		ID: "msg-1", ConversationID: conv.ID, TopicID: &topicID,
		Role: RoleUser, Content: "Hello", TokenCount: 10,
	}
	msg2 := &Message{
		ID: "msg-2", ConversationID: conv.ID, TopicID: &topicID,
		Role: RoleAssistant, Content: "Hi there!", TokenCount: 15,
	}
	if err := db.CreateMessage(ctx, msg1); err != nil {
		t.Fatalf("CreateMessage msg1: %v", err)
	}
	if err := db.CreateMessage(ctx, msg2); err != nil {
		t.Fatalf("CreateMessage msg2: %v", err)
	}

	// List messages
	msgs, err := db.ListMessages(ctx, conv.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}

	// List by topic
	topicMsgs, err := db.ListMessagesByTopic(ctx, topicID)
	if err != nil {
		t.Fatalf("ListMessagesByTopic: %v", err)
	}
	if len(topicMsgs) != 2 {
		t.Errorf("expected 2 messages for topic, got %d", len(topicMsgs))
	}

	// Get single message
	got, err := db.GetMessage(ctx, "msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Content != "Hello" {
		t.Errorf("expected content=%q, got %q", "Hello", got.Content)
	}

	// Update
	got.TokenCount = 20
	if err := db.UpdateMessage(ctx, got); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}

	// Delete
	if err := db.DeleteMessage(ctx, "msg-2"); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	afterDelete, err := db.ListMessages(ctx, conv.ID)
	if err != nil {
		t.Fatalf("ListMessages after delete: %v", err)
	}
	if len(afterDelete) != 1 {
		t.Errorf("expected 1 message after delete, got %d", len(afterDelete))
	}
}

func TestArchiveAndRestoreTopic(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	conv := createTestConversation(t, db)

	// Create topic with messages
	topicID := "topic-archive"
	topic := &Topic{
		ID: topicID, ConversationID: conv.ID,
		Name: "Archivable", Keywords: []string{"test"},
		RelevanceScore: 0.3,
	}
	if err := db.CreateTopic(ctx, topic); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	msg1 := &Message{
		ID: "amsg-1", ConversationID: conv.ID, TopicID: &topicID,
		Role: RoleUser, Content: "Original user message", TokenCount: 100,
	}
	msg2 := &Message{
		ID: "amsg-2", ConversationID: conv.ID, TopicID: &topicID,
		Role: RoleAssistant, Content: "Original assistant reply", TokenCount: 150,
	}
	if err := db.CreateMessage(ctx, msg1); err != nil {
		t.Fatalf("CreateMessage 1: %v", err)
	}
	if err := db.CreateMessage(ctx, msg2); err != nil {
		t.Fatalf("CreateMessage 2: %v", err)
	}

	// --- Archive ---
	if err := db.ArchiveTopic(ctx, topicID, 0.90, 0.60); err != nil {
		t.Fatalf("ArchiveTopic: %v", err)
	}

	// Topic should be archived
	archivedTopic, err := db.GetTopic(ctx, topicID)
	if err != nil {
		t.Fatalf("GetTopic after archive: %v", err)
	}
	if archivedTopic.ArchivedAt == nil {
		t.Error("expected ArchivedAt to be set after archive")
	}
	if archivedTopic.IsCurrent {
		t.Error("expected IsCurrent=false after archive")
	}

	// Messages should be marked archived
	activeMsgs, err := db.ListActiveMessages(ctx, conv.ID)
	if err != nil {
		t.Fatalf("ListActiveMessages: %v", err)
	}
	if len(activeMsgs) != 0 {
		t.Errorf("expected 0 active messages after archive, got %d", len(activeMsgs))
	}

	// Full content should be in message_archive
	archive1, err := db.GetArchivedContent(ctx, "amsg-1")
	if err != nil {
		t.Fatalf("GetArchivedContent: %v", err)
	}
	if archive1 == nil {
		t.Fatal("expected archived content for amsg-1, got nil")
	}
	if archive1.FullContent != "Original user message" {
		t.Errorf("expected preserved content=%q, got %q", "Original user message", archive1.FullContent)
	}

	// Archive event should be recorded
	events, err := db.ListArchiveEvents(ctx, conv.ID, 10)
	if err != nil {
		t.Fatalf("ListArchiveEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 archive event, got %d", len(events))
	}
	if events[0].Action != ArchiveActionArchive {
		t.Errorf("expected action=%q, got %q", ArchiveActionArchive, events[0].Action)
	}
	if events[0].TokensAffected != 250 {
		t.Errorf("expected tokens_affected=250, got %d", events[0].TokensAffected)
	}

	// --- Restore ---
	if err := db.RestoreTopic(ctx, topicID, 0.60, 0.85); err != nil {
		t.Fatalf("RestoreTopic: %v", err)
	}

	// Topic should no longer be archived
	restoredTopic, err := db.GetTopic(ctx, topicID)
	if err != nil {
		t.Fatalf("GetTopic after restore: %v", err)
	}
	if restoredTopic.ArchivedAt != nil {
		t.Error("expected ArchivedAt=nil after restore")
	}

	// Messages should be active again
	activeMsgsAfter, err := db.ListActiveMessages(ctx, conv.ID)
	if err != nil {
		t.Fatalf("ListActiveMessages after restore: %v", err)
	}
	if len(activeMsgsAfter) != 2 {
		t.Errorf("expected 2 active messages after restore, got %d", len(activeMsgsAfter))
	}

	// Content should be restored from archive
	restoredMsg, err := db.GetMessage(ctx, "amsg-1")
	if err != nil {
		t.Fatalf("GetMessage after restore: %v", err)
	}
	if restoredMsg.Content != "Original user message" {
		t.Errorf("expected restored content=%q, got %q", "Original user message", restoredMsg.Content)
	}

	// Archive records should be cleaned up
	archive1After, err := db.GetArchivedContent(ctx, "amsg-1")
	if err != nil {
		t.Fatalf("GetArchivedContent after restore: %v", err)
	}
	if archive1After != nil {
		t.Error("expected archive record to be deleted after restore")
	}

	// Should now have 2 events (archive + restore)
	events2, err := db.ListArchiveEvents(ctx, conv.ID, 10)
	if err != nil {
		t.Fatalf("ListArchiveEvents after restore: %v", err)
	}
	if len(events2) != 2 {
		t.Errorf("expected 2 archive events, got %d", len(events2))
	}
}

func TestRestoreNonArchivedTopicFails(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	conv := createTestConversation(t, db)

	topic := &Topic{
		ID: "active-topic", ConversationID: conv.ID,
		Name: "Active", Keywords: []string{},
	}
	if err := db.CreateTopic(ctx, topic); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	err := db.RestoreTopic(ctx, "active-topic", 0.50, 0.70)
	if err == nil {
		t.Error("expected error restoring non-archived topic")
	}
}

func TestGetActiveTopics(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	conv := createTestConversation(t, db)

	now := time.Now()

	active := &Topic{ID: "active", ConversationID: conv.ID, Name: "Active", Keywords: []string{}}
	archived := &Topic{ID: "archived", ConversationID: conv.ID, Name: "Archived", Keywords: []string{}, ArchivedAt: &now}

	if err := db.CreateTopic(ctx, active); err != nil {
		t.Fatalf("CreateTopic active: %v", err)
	}
	if err := db.CreateTopic(ctx, archived); err != nil {
		t.Fatalf("CreateTopic archived: %v", err)
	}

	topics, err := db.GetActiveTopics(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetActiveTopics: %v", err)
	}
	if len(topics) != 1 {
		t.Errorf("expected 1 active topic, got %d", len(topics))
	}
	if topics[0].ID != "active" {
		t.Errorf("expected active topic, got %q", topics[0].ID)
	}
}

func TestConversationStats(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	conv := createTestConversation(t, db)

	topicID := "stats-topic"
	topic := &Topic{ID: topicID, ConversationID: conv.ID, Name: "Stats", Keywords: []string{}}
	if err := db.CreateTopic(ctx, topic); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	for i, tc := range []struct {
		id         string
		role       Role
		tokens     int
		isArchived bool
	}{
		{"s1", RoleUser, 100, false},
		{"s2", RoleAssistant, 200, false},
		{"s3", RoleUser, 50, true},
	} {
		msg := &Message{
			ID: tc.id, ConversationID: conv.ID, TopicID: &topicID,
			Role: tc.role, Content: "msg", TokenCount: tc.tokens,
			IsArchived: tc.isArchived,
		}
		if err := db.CreateMessage(ctx, msg); err != nil {
			t.Fatalf("CreateMessage[%d]: %v", i, err)
		}
	}

	stats, err := db.GetConversationStats(ctx, conv.ID, 200000)
	if err != nil {
		t.Fatalf("GetConversationStats: %v", err)
	}

	if stats.MessageCount != 3 {
		t.Errorf("expected MessageCount=3, got %d", stats.MessageCount)
	}
	if stats.TotalTokens != 350 {
		t.Errorf("expected TotalTokens=350, got %d", stats.TotalTokens)
	}
	if stats.ArchivedTokens != 50 {
		t.Errorf("expected ArchivedTokens=50, got %d", stats.ArchivedTokens)
	}
	if stats.ActiveTokens != 300 {
		t.Errorf("expected ActiveTokens=300, got %d", stats.ActiveTokens)
	}
}

func TestCascadeDeleteConversation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	conv := createTestConversation(t, db)

	topicID := "cascade-topic"
	topic := &Topic{ID: topicID, ConversationID: conv.ID, Name: "Cascade", Keywords: []string{}}
	if err := db.CreateTopic(ctx, topic); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	msg := &Message{
		ID: "cascade-msg", ConversationID: conv.ID, TopicID: &topicID,
		Role: RoleUser, Content: "test", TokenCount: 10,
	}
	if err := db.CreateMessage(ctx, msg); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Delete the conversation — topics and messages should cascade
	if err := db.DeleteConversation(ctx, conv.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	topics, err := db.ListTopics(ctx, conv.ID)
	if err != nil {
		t.Fatalf("ListTopics after cascade: %v", err)
	}
	if len(topics) != 0 {
		t.Errorf("expected 0 topics after cascade delete, got %d", len(topics))
	}

	msgs, err := db.ListMessages(ctx, conv.ID)
	if err != nil {
		t.Fatalf("ListMessages after cascade: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after cascade delete, got %d", len(msgs))
	}
}

func TestUpdateTopicTokenCount(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	conv := createTestConversation(t, db)

	topicID := "token-topic"
	topic := &Topic{ID: topicID, ConversationID: conv.ID, Name: "Tokens", Keywords: []string{}}
	if err := db.CreateTopic(ctx, topic); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	// Add messages with known token counts
	for _, tc := range []struct {
		id     string
		tokens int
	}{
		{"tk1", 100},
		{"tk2", 200},
		{"tk3", 300},
	} {
		msg := &Message{
			ID: tc.id, ConversationID: conv.ID, TopicID: &topicID,
			Role: RoleUser, Content: "x", TokenCount: tc.tokens,
		}
		if err := db.CreateMessage(ctx, msg); err != nil {
			t.Fatalf("CreateMessage %s: %v", tc.id, err)
		}
	}

	// Recalculate
	if err := db.UpdateTopicTokenCount(ctx, topicID); err != nil {
		t.Fatalf("UpdateTopicTokenCount: %v", err)
	}

	got, err := db.GetTopic(ctx, topicID)
	if err != nil {
		t.Fatalf("GetTopic: %v", err)
	}
	if got.TokenCount != 600 {
		t.Errorf("expected TokenCount=600, got %d", got.TokenCount)
	}
}
