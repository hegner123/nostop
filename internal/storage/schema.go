// Package storage provides SQLite storage for the nostop system.
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
    role TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'system')),
    content TEXT NOT NULL,   -- JSON array of content blocks
    token_count INTEGER DEFAULT 0,
    is_archived BOOLEAN DEFAULT FALSE,
    created_at DATETIME NOT NULL,
    -- Summary message fields (Phase A: Summary-on-Archive)
    is_summary BOOLEAN DEFAULT FALSE,
    summary_source TEXT,           -- 'topic' or 'work_unit'
    summary_source_id TEXT         -- topic_id or work_unit_id being summarized
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
CREATE INDEX IF NOT EXISTS idx_messages_summary ON messages(is_summary);
CREATE INDEX IF NOT EXISTS idx_messages_summary_source ON messages(summary_source_id);
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
	// Migration 0: Add summary columns to messages table (Phase A: Summary-on-Archive)
	`
	-- Add is_summary column if it doesn't exist
	ALTER TABLE messages ADD COLUMN is_summary BOOLEAN DEFAULT FALSE;
	`,
	`
	-- Add summary_source column if it doesn't exist
	ALTER TABLE messages ADD COLUMN summary_source TEXT;
	`,
	`
	-- Add summary_source_id column if it doesn't exist
	ALTER TABLE messages ADD COLUMN summary_source_id TEXT;
	`,
	// Migration 3: Add 'system' role to messages CHECK constraint
	// Note: SQLite doesn't support ALTER COLUMN, so we handle this in code
	// by accepting 'system' role in the CHECK constraint in the base schema
}

// MigrationVersion returns the current schema version.
func MigrationVersion() int {
	return len(Migrations)
}
