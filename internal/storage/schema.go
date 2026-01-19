// Package storage provides SQLite storage for the RLM system.
package storage

// Schema contains the SQL statements for creating the database schema.
const Schema = `
-- Conversations table
CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    title TEXT,
    model TEXT,
    system_prompt TEXT,
    total_token_count INTEGER DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

-- Topics within conversations
CREATE TABLE IF NOT EXISTS topics (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    keywords TEXT,           -- JSON array of keywords
    token_count INTEGER DEFAULT 0,
    relevance_score REAL DEFAULT 1.0,    -- 0.0-1.0
    is_current BOOLEAN DEFAULT FALSE,
    archived_at DATETIME,    -- NULL if active
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

-- Messages belong to topics
CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    topic_id TEXT REFERENCES topics(id) ON DELETE SET NULL,
    role TEXT NOT NULL CHECK(role IN ('user', 'assistant')),
    content TEXT NOT NULL,   -- JSON array of content blocks
    token_count INTEGER DEFAULT 0,
    is_archived BOOLEAN DEFAULT FALSE,
    created_at DATETIME NOT NULL
);

-- Archive storage (full message content for archived topics)
CREATE TABLE IF NOT EXISTS message_archive (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    topic_id TEXT NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    full_content TEXT NOT NULL,  -- Complete message preserved
    archived_at DATETIME NOT NULL
);

-- Archival history for debugging/analytics
CREATE TABLE IF NOT EXISTS archive_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    topic_id TEXT NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK(action IN ('archive', 'restore')),
    tokens_affected INTEGER NOT NULL,
    context_usage_before REAL NOT NULL,
    context_usage_after REAL NOT NULL,
    created_at DATETIME NOT NULL
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_messages_topic ON messages(topic_id);
CREATE INDEX IF NOT EXISTS idx_messages_archived ON messages(is_archived);
CREATE INDEX IF NOT EXISTS idx_topics_conversation ON topics(conversation_id);
CREATE INDEX IF NOT EXISTS idx_topics_archived ON topics(archived_at);
CREATE INDEX IF NOT EXISTS idx_topics_current ON topics(is_current);
CREATE INDEX IF NOT EXISTS idx_message_archive_message ON message_archive(message_id);
CREATE INDEX IF NOT EXISTS idx_message_archive_topic ON message_archive(topic_id);
CREATE INDEX IF NOT EXISTS idx_archive_events_conversation ON archive_events(conversation_id);
CREATE INDEX IF NOT EXISTS idx_archive_events_topic ON archive_events(topic_id);
`

// Migrations holds incremental schema updates for future versions.
// Each migration should be idempotent (safe to run multiple times).
var Migrations = []string{
	// Migration 0: Initial schema (applied via Schema constant above)
	// Future migrations can be added here as the schema evolves
}

// MigrationVersion returns the current schema version.
func MigrationVersion() int {
	return len(Migrations)
}
