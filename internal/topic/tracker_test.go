package topic

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/user/rlm/internal/storage"
)

func setupTestDB(t *testing.T) (*storage.SQLite, func()) {
	t.Helper()

	// Create temp file for test database
	f, err := os.CreateTemp("", "tracker_test_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	dbPath := f.Name()
	f.Close()

	db, err := storage.NewSQLite(dbPath)
	if err != nil {
		os.Remove(dbPath)
		t.Fatalf("failed to create SQLite: %v", err)
	}

	if err := db.InitSchema(); err != nil {
		db.Close()
		os.Remove(dbPath)
		t.Fatalf("failed to init schema: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(dbPath)
	}

	return db, cleanup
}

func TestNewTopicTracker(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewTopicTracker(db)
	if tracker == nil {
		t.Fatal("expected non-nil tracker")
	}

	if tracker.storage != db {
		t.Error("storage not set correctly")
	}

	if tracker.topics == nil {
		t.Error("topics should be initialized")
	}

	if tracker.allocations == nil {
		t.Error("allocations should be initialized")
	}
}

func TestLoadTopics(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create a conversation
	conv := &storage.Conversation{
		Title: "Test Conversation",
		Model: "claude-3-sonnet",
	}
	if err := db.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("failed to create conversation: %v", err)
	}

	// Create topics
	topic1 := &storage.Topic{
		ConversationID: conv.ID,
		Name:           "Topic 1",
		TokenCount:     100,
		RelevanceScore: 0.8,
		IsCurrent:      false,
	}
	topic2 := &storage.Topic{
		ConversationID: conv.ID,
		Name:           "Topic 2",
		TokenCount:     200,
		RelevanceScore: 0.9,
		IsCurrent:      true,
	}

	if err := db.CreateTopic(ctx, topic1); err != nil {
		t.Fatalf("failed to create topic1: %v", err)
	}
	if err := db.CreateTopic(ctx, topic2); err != nil {
		t.Fatalf("failed to create topic2: %v", err)
	}

	// Create tracker and load topics
	tracker := NewTopicTracker(db)
	if err := tracker.LoadTopics(ctx, conv.ID); err != nil {
		t.Fatalf("failed to load topics: %v", err)
	}

	// Verify topics loaded
	if len(tracker.topics) != 2 {
		t.Errorf("expected 2 topics, got %d", len(tracker.topics))
	}

	// Verify current topic is set
	if tracker.currentID != topic2.ID {
		t.Errorf("expected current topic to be %s, got %s", topic2.ID, tracker.currentID)
	}

	// Verify allocations were calculated
	if len(tracker.allocations) == 0 {
		t.Error("expected allocations to be calculated")
	}
}

func TestRecalculateAllocations(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewTopicTracker(db)

	now := time.Now()

	// Add topics directly for testing
	tracker.topics = []*storage.Topic{
		{
			ID:             "topic-1",
			Name:           "Current Topic",
			TokenCount:     1000,
			RelevanceScore: 1.0,
			UpdatedAt:      now,
		},
		{
			ID:             "topic-2",
			Name:           "Recent Topic",
			TokenCount:     500,
			RelevanceScore: 0.7,
			UpdatedAt:      now.Add(-1 * time.Hour),
		},
		{
			ID:             "topic-3",
			Name:           "Old Topic",
			TokenCount:     300,
			RelevanceScore: 0.3,
			UpdatedAt:      now.Add(-20 * time.Hour),
		},
	}

	allocations := tracker.RecalculateAllocations("topic-1")

	// Current topic should get the highest allocation
	if allocations["topic-1"] <= allocations["topic-2"] {
		t.Errorf("current topic should have higher allocation than recent topic")
	}
	if allocations["topic-2"] <= allocations["topic-3"] {
		t.Errorf("recent topic should have higher allocation than old topic")
	}

	// Total should be 90% (10% reserved for system)
	var total float64
	for _, alloc := range allocations {
		total += alloc
	}
	if total < 0.89 || total > 0.91 {
		t.Errorf("expected total allocation ~0.90, got %f", total)
	}
}

func TestGetTopicsToArchive(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewTopicTracker(db)

	now := time.Now()

	// Set up topics with varying relevance and age
	tracker.topics = []*storage.Topic{
		{
			ID:             "current",
			Name:           "Current Topic",
			TokenCount:     1000,
			RelevanceScore: 1.0,
			IsCurrent:      true,
			UpdatedAt:      now,
		},
		{
			ID:             "recent-high",
			Name:           "Recent High Relevance",
			TokenCount:     500,
			RelevanceScore: 0.9,
			UpdatedAt:      now.Add(-1 * time.Hour),
		},
		{
			ID:             "old-low",
			Name:           "Old Low Relevance",
			TokenCount:     800,
			RelevanceScore: 0.2,
			UpdatedAt:      now.Add(-48 * time.Hour),
		},
		{
			ID:             "mid-mid",
			Name:           "Mid Relevance",
			TokenCount:     400,
			RelevanceScore: 0.5,
			UpdatedAt:      now.Add(-12 * time.Hour),
		},
	}
	tracker.currentID = "current"

	// Test below threshold - should return nil
	toArchive := tracker.GetTopicsToArchive(0.80)
	if toArchive != nil {
		t.Error("should not archive below 95% threshold")
	}

	// Test at threshold
	toArchive = tracker.GetTopicsToArchive(0.95)
	if len(toArchive) == 0 {
		t.Error("expected topics to archive at 95%")
	}

	// Current topic should never be archived
	for _, topic := range toArchive {
		if topic.ID == "current" {
			t.Error("current topic should never be archived")
		}
	}

	// Lowest relevance topics should be first
	if len(toArchive) > 0 && toArchive[0].ID != "old-low" {
		t.Errorf("expected old-low to be archived first, got %s", toArchive[0].ID)
	}
}

func TestAddTopic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create a conversation
	conv := &storage.Conversation{
		Title: "Test Conversation",
		Model: "claude-3-sonnet",
	}
	if err := db.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("failed to create conversation: %v", err)
	}

	tracker := NewTopicTracker(db)

	topic := &storage.Topic{
		ConversationID: conv.ID,
		Name:           "New Topic",
		TokenCount:     100,
		RelevanceScore: 1.0,
	}

	if err := tracker.AddTopic(ctx, topic); err != nil {
		t.Fatalf("failed to add topic: %v", err)
	}

	if len(tracker.topics) != 1 {
		t.Errorf("expected 1 topic, got %d", len(tracker.topics))
	}

	// Verify persisted
	stored, err := db.GetTopic(ctx, topic.ID)
	if err != nil {
		t.Fatalf("failed to get topic from db: %v", err)
	}
	if stored == nil {
		t.Error("topic should be persisted")
	}
	if stored.Name != "New Topic" {
		t.Errorf("expected name 'New Topic', got %s", stored.Name)
	}
}

func TestSetCurrentTopic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create a conversation
	conv := &storage.Conversation{
		Title: "Test Conversation",
		Model: "claude-3-sonnet",
	}
	if err := db.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("failed to create conversation: %v", err)
	}

	// Create topics
	topic1 := &storage.Topic{
		ConversationID: conv.ID,
		Name:           "Topic 1",
		TokenCount:     100,
		RelevanceScore: 0.8,
		IsCurrent:      true,
	}
	topic2 := &storage.Topic{
		ConversationID: conv.ID,
		Name:           "Topic 2",
		TokenCount:     200,
		RelevanceScore: 0.9,
	}

	tracker := NewTopicTracker(db)
	if err := tracker.AddTopic(ctx, topic1); err != nil {
		t.Fatalf("failed to add topic1: %v", err)
	}
	if err := tracker.AddTopic(ctx, topic2); err != nil {
		t.Fatalf("failed to add topic2: %v", err)
	}

	// Set topic2 as current
	if err := tracker.SetCurrentTopic(ctx, topic2.ID); err != nil {
		t.Fatalf("failed to set current topic: %v", err)
	}

	// Verify tracker state
	if tracker.currentID != topic2.ID {
		t.Errorf("expected currentID to be %s, got %s", topic2.ID, tracker.currentID)
	}

	// Verify GetCurrentTopic
	current := tracker.GetCurrentTopic()
	if current == nil {
		t.Fatal("expected current topic")
	}
	if current.ID != topic2.ID {
		t.Errorf("expected current topic ID %s, got %s", topic2.ID, current.ID)
	}
}

func TestGetAllocation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewTopicTracker(db)
	tracker.topics = []*storage.Topic{
		{
			ID:             "topic-1",
			Name:           "Topic 1",
			TokenCount:     100,
			RelevanceScore: 1.0,
			UpdatedAt:      time.Now(),
		},
	}

	tracker.RecalculateAllocations("topic-1")

	alloc := tracker.GetAllocation("topic-1")
	if alloc <= 0 {
		t.Error("expected positive allocation")
	}

	// Non-existent topic should return 0
	alloc = tracker.GetAllocation("non-existent")
	if alloc != 0 {
		t.Error("expected 0 for non-existent topic")
	}
}

func TestUpdateRelevance(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create a conversation
	conv := &storage.Conversation{
		Title: "Test Conversation",
		Model: "claude-3-sonnet",
	}
	if err := db.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("failed to create conversation: %v", err)
	}

	tracker := NewTopicTracker(db)

	topic := &storage.Topic{
		ConversationID: conv.ID,
		Name:           "Test Topic",
		TokenCount:     100,
		RelevanceScore: 0.5,
	}
	if err := tracker.AddTopic(ctx, topic); err != nil {
		t.Fatalf("failed to add topic: %v", err)
	}

	// Update relevance
	if err := tracker.UpdateRelevance(ctx, topic.ID, 0.9); err != nil {
		t.Fatalf("failed to update relevance: %v", err)
	}

	// Verify in tracker
	for _, tp := range tracker.topics {
		if tp.ID == topic.ID {
			if tp.RelevanceScore != 0.9 {
				t.Errorf("expected relevance 0.9, got %f", tp.RelevanceScore)
			}
		}
	}

	// Verify persisted
	stored, err := db.GetTopic(ctx, topic.ID)
	if err != nil {
		t.Fatalf("failed to get topic: %v", err)
	}
	if stored.RelevanceScore != 0.9 {
		t.Errorf("expected stored relevance 0.9, got %f", stored.RelevanceScore)
	}

	// Test clamping - above 1.0
	if err := tracker.UpdateRelevance(ctx, topic.ID, 1.5); err != nil {
		t.Fatalf("failed to update relevance: %v", err)
	}
	stored, _ = db.GetTopic(ctx, topic.ID)
	if stored.RelevanceScore != 1.0 {
		t.Errorf("expected clamped relevance 1.0, got %f", stored.RelevanceScore)
	}

	// Test clamping - below 0
	if err := tracker.UpdateRelevance(ctx, topic.ID, -0.5); err != nil {
		t.Fatalf("failed to update relevance: %v", err)
	}
	stored, _ = db.GetTopic(ctx, topic.ID)
	if stored.RelevanceScore != 0.0 {
		t.Errorf("expected clamped relevance 0.0, got %f", stored.RelevanceScore)
	}
}

func TestTopicCountAndTotalTokens(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewTopicTracker(db)
	tracker.topics = []*storage.Topic{
		{ID: "1", Name: "T1", TokenCount: 100},
		{ID: "2", Name: "T2", TokenCount: 200},
		{ID: "3", Name: "T3", TokenCount: 300},
	}

	if tracker.TopicCount() != 3 {
		t.Errorf("expected 3 topics, got %d", tracker.TopicCount())
	}

	if tracker.TotalTokens() != 600 {
		t.Errorf("expected 600 tokens, got %d", tracker.TotalTokens())
	}
}

func TestRemoveTopic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewTopicTracker(db)

	now := time.Now()
	tracker.topics = []*storage.Topic{
		{ID: "1", Name: "T1", TokenCount: 100, UpdatedAt: now.Add(-1 * time.Hour)},
		{ID: "2", Name: "T2", TokenCount: 200, UpdatedAt: now},
	}
	tracker.currentID = "1"
	tracker.RecalculateAllocations("1")

	tracker.RemoveTopic("1")

	if len(tracker.topics) != 1 {
		t.Errorf("expected 1 topic, got %d", len(tracker.topics))
	}

	if _, exists := tracker.allocations["1"]; exists {
		t.Error("allocation for removed topic should be deleted")
	}

	// Current should be set to the remaining topic
	if tracker.currentID != "2" {
		t.Errorf("expected current to switch to '2', got %s", tracker.currentID)
	}
}

func TestGetTopics(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewTopicTracker(db)
	tracker.topics = []*storage.Topic{
		{ID: "1", Name: "T1"},
		{ID: "2", Name: "T2"},
	}

	topics := tracker.GetTopics()
	if len(topics) != 2 {
		t.Errorf("expected 2 topics, got %d", len(topics))
	}

	// Verify it's a new slice (modifications to the slice itself don't affect original)
	originalLen := len(tracker.topics)
	topics = append(topics, &storage.Topic{ID: "3", Name: "T3"})
	if len(tracker.topics) != originalLen {
		t.Error("GetTopics should return a copy of the slice, not the original")
	}
}

func TestGetTopic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewTopicTracker(db)
	tracker.topics = []*storage.Topic{
		{ID: "1", Name: "T1"},
		{ID: "2", Name: "T2"},
	}

	topic := tracker.GetTopic("1")
	if topic == nil {
		t.Fatal("expected to find topic")
	}
	if topic.Name != "T1" {
		t.Errorf("expected name 'T1', got %s", topic.Name)
	}

	// Non-existent
	topic = tracker.GetTopic("non-existent")
	if topic != nil {
		t.Error("expected nil for non-existent topic")
	}
}
