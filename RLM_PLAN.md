# RLM (Recursive Language Model) System Plan

## Overview

A Go library + CLI for intelligent **topic-based context archival** before sending messages to Claude. The system tracks conversation topics, dynamically adjusts context allocation as topics shift, and archives older topics to SQLite when approaching token limits.

**Key principle**: Archive, don't compact. Older topics are preserved in full but moved to storage, not summarized.

## Architecture

```
rlm/
├── cmd/
│   └── rlm/
│       └── main.go              # CLI entry point
├── internal/
│   ├── api/
│   │   ├── client.go            # Claude API client
│   │   ├── types.go             # API types (Message, ContentBlock, etc.)
│   │   └── stream.go            # SSE streaming handler
│   ├── storage/
│   │   ├── sqlite.go            # SQLite operations
│   │   ├── schema.go            # DB schema/migrations
│   │   └── models.go            # DB models
│   ├── topic/
│   │   ├── detector.go          # Claude-based topic detection
│   │   ├── tracker.go           # Topic state management
│   │   └── scorer.go            # Relevance scoring via Claude
│   └── tui/
│       ├── app.go               # Bubbletea main model
│       ├── chat.go              # Chat view component
│       ├── history.go           # Conversation history view
│       └── styles.go            # Lipgloss styles
├── pkg/
│   └── rlm/
│       ├── rlm.go               # Main RLM engine
│       ├── context.go           # Context budget management
│       ├── conversation.go      # Conversation state
│       └── archiver.go          # Topic archival logic
├── go.mod
└── go.sum
```

## Core Components

### 1. Topic Detection (`internal/topic/detector.go`)

Uses Claude to identify and track conversation topics:

```go
type TopicDetector struct {
    client *api.Client
    model  string  // Use fast model (haiku) for detection
}

type Topic struct {
    ID          string
    Name        string
    Keywords    []string
    StartMsgID  string
    EndMsgID    string    // nil if topic is current
    TokenCount  int
    Relevance   float64   // 0.0-1.0, current relevance score
    CreatedAt   time.Time
    ArchivedAt  *time.Time
}

// DetectTopicShift analyzes recent messages to identify topic changes
func (td *TopicDetector) DetectTopicShift(ctx context.Context, recent []Message) (*TopicShift, error)

// ScoreRelevance uses Claude to score how relevant a topic is to current context
func (td *TopicDetector) ScoreRelevance(ctx context.Context, topic Topic, currentQuery string) (float64, error)
```

### 2. Topic Tracker (`internal/topic/tracker.go`)

Manages topic state and context allocation:

```go
type TopicTracker struct {
    topics      []Topic
    allocations map[string]float64  // topic ID -> % of context budget
}

// Algorithm for context allocation:
// 1. Current topic gets base allocation (e.g., 60%)
// 2. Recent related topics share remaining (e.g., 30%)
// 3. Reserve 10% for system prompt + overhead
// 4. As relevance drops, allocation shrinks proportionally

func (tt *TopicTracker) RecalculateAllocations(currentTopic string) map[string]float64
func (tt *TopicTracker) GetTopicsToArchive(usagePercent float64) []Topic
```

### 3. Archiver (`pkg/rlm/archiver.go`)

Handles moving topics to/from SQLite storage:

```go
type Archiver struct {
    storage *storage.SQLite
    tracker *TopicTracker
}

// Archive moves a topic's messages to cold storage
// Messages are preserved in full, not summarized
func (a *Archiver) ArchiveTopic(ctx context.Context, topic Topic) error

// Restore brings archived topic back into active context
// Used when user references archived content
func (a *Archiver) RestoreTopic(ctx context.Context, topicID string) (*Topic, error)

// ArchiveUntilTarget archives topics until context usage drops to target%
func (a *Archiver) ArchiveUntilTarget(ctx context.Context, targetPercent float64) ([]Topic, error)
```

### 4. Context Budget Rules

```
Context Usage Thresholds:
├── 0-50%   → Normal operation, no archival
├── 50-80%  → Monitor, recalculate allocations more frequently
├── 80-95%  → Warning zone, prepare topics for potential archival
└── 95%+    → TRIGGER: Archive lowest-relevance topics until 50% free

Allocation Formula:
  topic_allocation = base_weight * relevance_score * recency_factor

Where:
  base_weight = 1.0 for current topic, 0.5 for others
  relevance_score = Claude-scored 0.0-1.0
  recency_factor = 1.0 - (hours_since_active / 24), min 0.1
```

### 5. SQLite Schema

```sql
-- Conversations
CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    title TEXT,
    model TEXT,
    system_prompt TEXT,
    total_token_count INTEGER DEFAULT 0,
    created_at DATETIME,
    updated_at DATETIME
);

-- Topics within conversations
CREATE TABLE topics (
    id TEXT PRIMARY KEY,
    conversation_id TEXT REFERENCES conversations(id),
    name TEXT,
    keywords TEXT,           -- JSON array
    token_count INTEGER,
    relevance_score REAL,    -- 0.0-1.0
    is_current BOOLEAN DEFAULT FALSE,
    archived_at DATETIME,    -- NULL if active
    created_at DATETIME,
    updated_at DATETIME
);

-- Messages belong to topics
CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT REFERENCES conversations(id),
    topic_id TEXT REFERENCES topics(id),
    role TEXT CHECK(role IN ('user', 'assistant')),
    content TEXT,            -- JSON array of content blocks
    token_count INTEGER,
    is_archived BOOLEAN DEFAULT FALSE,
    created_at DATETIME
);

-- Archive storage (full message content for archived topics)
CREATE TABLE message_archive (
    id TEXT PRIMARY KEY,
    message_id TEXT REFERENCES messages(id),
    topic_id TEXT REFERENCES topics(id),
    full_content TEXT,       -- Complete message preserved
    archived_at DATETIME
);

-- Archival history for debugging/analytics
CREATE TABLE archive_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT REFERENCES conversations(id),
    topic_id TEXT REFERENCES topics(id),
    action TEXT CHECK(action IN ('archive', 'restore')),
    tokens_affected INTEGER,
    context_usage_before REAL,
    context_usage_after REAL,
    created_at DATETIME
);

-- Indexes for performance
CREATE INDEX idx_messages_topic ON messages(topic_id);
CREATE INDEX idx_messages_archived ON messages(is_archived);
CREATE INDEX idx_topics_conversation ON topics(conversation_id);
CREATE INDEX idx_topics_archived ON topics(archived_at);
```

### 6. RLM Engine (`pkg/rlm/rlm.go`)

```go
type RLM struct {
    client   *api.Client
    storage  *storage.SQLite
    detector *topic.Detector
    tracker  *topic.Tracker
    archiver *Archiver
    config   Config
}

type Config struct {
    MaxContextTokens  int     // Model's context limit (e.g., 200000)
    ArchiveThreshold  float64 // Trigger archival at this % (default: 0.95)
    ArchiveTarget     float64 // Archive until this % free (default: 0.50)
    DetectionModel    string  // Model for topic detection (haiku)
    ChatModel         string  // Model for chat (sonnet/opus)
    APIKey            string
    DBPath            string
}

// Core methods
func (r *RLM) Send(ctx context.Context, convID, message string) (*Response, error) {
    // 1. Add user message to current topic
    // 2. Check for topic shift (async, via haiku)
    // 3. Calculate context usage
    // 4. If >= 95%, archive until 50% free
    // 5. Build context from active topics
    // 6. Send to Claude, stream response
    // 7. Store assistant response
}

func (r *RLM) BuildContext(ctx context.Context, conv *Conversation) ([]api.Message, error) {
    // Only includes non-archived topics
    // Respects allocation percentages
}
```

### 7. Context Manager (`pkg/rlm/context.go`)

```go
type ContextManager struct {
    maxTokens int
    tracker   *topic.Tracker
    client    *api.Client
}

func (cm *ContextManager) GetUsage(ctx context.Context, conv *Conversation) (*ContextUsage, error)

type ContextUsage struct {
    TotalTokens    int
    MaxTokens      int
    UsagePercent   float64
    TopicBreakdown map[string]TopicUsage  // topic ID -> usage
}

type TopicUsage struct {
    TopicName   string
    TokenCount  int
    Allocation  float64  // Current % allocation
    Relevance   float64  // Current relevance score
    IsArchived  bool
}

func (cm *ContextManager) ShouldArchive() bool {
    return cm.UsagePercent >= 0.95
}
```

### 8. Bubbletea CLI

#### Views:
1. **Chat View** - Main conversation interface with topic indicator
2. **History View** - Browse past conversations
3. **Topics View** - See all topics, archived status, token usage
4. **Debug View** - Show context usage, topic allocations

#### Key bindings:
- `ctrl+n` - New conversation
- `ctrl+h` - History browser
- `ctrl+t` - Topics overview
- `ctrl+d` - Debug/context info
- `ctrl+r` - Restore archived topic (from topics view)

## Implementation Order

### Phase 1: Core Foundation
1. [ ] Project structure and go.mod
2. [ ] API types (`internal/api/types.go`) - Messages, ContentBlocks, etc.
3. [ ] API client (`internal/api/client.go`) - Messages + token counting
4. [ ] SSE streaming (`internal/api/stream.go`)
5. [ ] SQLite storage with schema (`internal/storage/`)

### Phase 2: Topic System
6. [ ] Topic detector (`internal/topic/detector.go`) - Claude-based detection
7. [ ] Topic tracker (`internal/topic/tracker.go`) - Allocation management
8. [ ] Relevance scorer (`internal/topic/scorer.go`) - Claude-based scoring

### Phase 3: Context Management
9. [ ] Context manager (`pkg/rlm/context.go`) - Usage tracking
10. [ ] Archiver (`pkg/rlm/archiver.go`) - Archive/restore logic
11. [ ] RLM engine (`pkg/rlm/rlm.go`) - Main orchestrator

### Phase 4: CLI
12. [ ] Bubbletea app scaffolding (`internal/tui/app.go`)
13. [ ] Chat view with streaming (`internal/tui/chat.go`)
14. [ ] History browser (`internal/tui/history.go`)
15. [ ] Topics view (`internal/tui/topics.go`)
16. [ ] Debug view (`internal/tui/debug.go`)

### Phase 5: Polish
17. [ ] Cache control integration (for system prompts)
18. [ ] Topic restoration UI
19. [ ] Configuration file support (TOML/JSON)
20. [ ] Graceful error handling and retries

## Key Design Decisions

### Token Counting
- Use Claude's `/v1/messages/count_tokens` endpoint for accuracy
- Cache token counts per-message in SQLite
- Recalculate topic totals on changes
- Track usage as percentage of model's max context

### Topic Detection
- Use Haiku for fast, cheap topic detection
- Detect shifts by analyzing last 3-5 messages
- Topics have fuzzy boundaries (messages can relate to multiple)
- Default: assign new messages to current topic unless shift detected

### Archival vs Summarization
- **Archive**: Move full messages to cold storage, remove from active context
- **No compression**: Archived content preserved verbatim
- **Restoration**: Bring entire topic back when referenced
- Rationale: Preserves fidelity, simpler than summarization

### Relevance Scoring
- Claude-based scoring (Haiku for cost efficiency)
- Score each topic against current conversation direction
- Factors: explicit references, keyword overlap, recency
- Lower-relevance topics archived first

### Cache Control
- System prompt: 1h TTL (rarely changes)
- Recent topics: 5m TTL
- No cache control on archived content (not sent)

## Dependencies

```go
require (
    github.com/charmbracelet/bubbletea v1.x
    github.com/charmbracelet/lipgloss v1.x
    github.com/charmbracelet/bubbles v0.x
    modernc.org/sqlite v1.x  // Pure Go SQLite, no CGO
)
```

Standard library for HTTP, JSON, SSE parsing.

## Verification

### Unit Tests
- Topic detector prompt construction
- Allocation calculation formulas
- Archive/restore DB operations
- API type marshaling/unmarshaling
- Context usage percentage calculations

### Integration Tests
- Full conversation with topic shifts
- Archival trigger at 95% threshold
- Topic restoration on reference
- Persistence across restarts

### Manual Testing
1. Start CLI: `go run ./cmd/rlm`
2. Have multi-topic conversation (e.g., discuss Go, then switch to Python)
3. Continue until context approaches 95%
4. Verify archival triggers, oldest/least-relevant topic archived
5. Reference archived topic, verify restoration prompt
6. Check `ctrl+t` topics view shows correct states
7. Check `ctrl+d` debug view shows context breakdown
8. Restart CLI, verify conversation and topics persist

### Example Test Scenario
```
User: Let's discuss Go error handling patterns    [Topic 1: Go]
...20 messages about Go...
User: Now let's talk about Python decorators      [Topic 2: Python - shift detected]
...30 messages about Python...
User: Actually, what was that Go pattern again?   [Reference to Topic 1]
→ System restores Topic 1, may archive Topic 2 if needed
```
