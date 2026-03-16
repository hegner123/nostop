# Context Management Plan

> Two-mode context management: ad-hoc topic archiving with summaries for
> exploratory sessions, and plan-driven work unit archiving for structured
> multi-step tasks. Both modes archive detail to SQLite and retain compact
> summaries in active context.

## Status: Phase D Complete (2025-01-XX)

---

## Use Cases

### Use Case 1: Ad-hoc Exploration

User explores a codebase, troubleshoots bugs, cleans up code across
multiple areas. Topics are detected automatically. When the user moves on,
old topics are archived with a summary that stays in active context.

**Current state:** Topics detect drift. Archiving creates summary messages.
**Gap:** None — Phase A complete.

### Use Case 2: Plan-Driven Execution

User defines a plan document with phases, steps, and tasks. Nostop parses
the plan into work units using a JSON schema. As steps complete, their
detailed messages are archived and replaced with a compact summary.

**Current state:** Does not exist.
**Gap:** Everything — schema, parser, work unit tracking, summary generation.

---

## Shared Foundation: Summary-on-Archive

Both use cases need the same core capability: when archiving a group of
messages, generate a summary and keep it as an active synthetic message.

### Summary message schema

```sql
-- New column on messages table
ALTER TABLE messages ADD COLUMN is_summary BOOLEAN DEFAULT FALSE;
ALTER TABLE messages ADD COLUMN summary_source TEXT; -- 'topic' or 'work_unit'
ALTER TABLE messages ADD COLUMN summary_source_id TEXT; -- topic_id or work_unit_id
```

A summary message:
- `role = 'system'`
- `is_archived = FALSE` (stays in active context)
- `is_summary = TRUE`
- `content` = compact text: what was done, what files were touched, outcomes
- `summary_source_id` = the topic or work unit it summarizes
- Links back to the archived detail via `summary_source_id`

### Summary generation

For v1, build summaries from structured data already in the messages:
- Count tool calls by type (read, edit, bash)
- Extract file paths from tool targets
- Keep the first and last assistant text (intent + conclusion)
- Format as a compact block:

```
[Archived: "SQL Schema Changes" — 12 messages, 1,847 tokens]
Files: schema.sql, migrations/003_add_status.sql
Actions: 3 reads, 2 edits, 4 bash (go build), 1 test pass
Result: Added 'status' column to users table, ran migrations,
fixed 12 build errors across 8 files.
```

This avoids an extra API call for summarization. If the structured summary
isn't rich enough, a future version can use Claude to generate it.

---

## Phase A: Summary-on-Archive (enhances Use Case 1) ✅ COMPLETE

**Goal:** When a topic is archived (manually or automatically), generate a
summary and keep it in active context.

### A.1 — Schema migration ✅

Add `is_summary`, `summary_source`, `summary_source_id` columns to
messages table. Migration must be idempotent (check before ALTER).

### A.2 — Summary builder ✅

New function in `pkg/nostop/`:

```go
func BuildArchiveSummary(messages []storage.Message, topicName string) string
```

Takes the messages about to be archived, extracts structured info (tool
calls, file paths, assistant text), formats a compact summary string.

### A.3 — Integrate into archive flow ✅

In `Archiver.ArchiveTopic()`:
1. Before marking messages as archived, call `BuildArchiveSummary()`
2. Insert a new summary message with `is_summary = TRUE`
3. Then archive the detail messages as before

The summary message has the same `conversation_id` and `topic_id` as the
archived messages, but `is_archived = FALSE` so it stays in context.

### A.4 — TUI display ✅

Show summary messages with a distinct style (collapsed/dimmed). The user
sees "[Archived: topic name — summary]" in the chat instead of nothing.

### Testing

- Archive a topic with 10+ messages → summary message created
- Summary message appears in `ListActiveMessages()` results
- Summary content includes file paths and action counts
- Restore topic → summary message can be removed or kept alongside detail

**Files:** `internal/storage/sqlite.go`, `pkg/nostop/archiver.go`,
`pkg/nostop/summary.go` (new), `internal/tui/chat.go`

---

## Phase B: Plan Schema and Parser (enables Use Case 2) ✅ COMPLETE

**Goal:** Define a JSON schema for plan structure, parse plan documents
into a work unit tree.

**Completed:** 2025-01-13. All items verified.

### B.1 — JSON schema definition ✅

File: `nostop-plan.json` in project root (or `.nostop/plan.json`).

```json
{
  "$schema": "nostop/plan/v1",
  "file": "CRUSH_PATTERNS_PLAN.md",
  "levels": [
    {
      "name": "phase",
      "marker": "heading",
      "depth": 2,
      "prefix": "Phase"
    },
    {
      "name": "step",
      "marker": "heading",
      "depth": 3,
      "prefix": ""
    },
    {
      "name": "item",
      "marker": "checklist",
      "depth": 0,
      "prefix": ""
    }
  ]
}
```

**Schema fields:**

| Field | Type | Description |
|-------|------|-------------|
| `file` | string | Path to the plan markdown file |
| `levels` | array | Hierarchy of work unit levels, ordered parent → child |
| `levels[].name` | string | User-chosen label for this level |
| `levels[].marker` | enum | `heading`, `checklist`, `numbered`, `bullet` |
| `levels[].depth` | int | For headings: `##` = 2, `###` = 3. For lists: nesting depth |
| `levels[].prefix` | string | Optional prefix filter (e.g. "Phase" skips non-phase H2s) |

### B.2 — Work unit types ✅

New package: `internal/plan/`

```go
type WorkUnit struct {
    ID       string       // derived: "phase-1/step-1.1"
    Name     string       // "Complete Charm v2 Migration"
    Level    string       // "phase", "step", "item"
    Status   UnitStatus   // Pending, Active, Complete, Archived
    Parent   string       // parent work unit ID, empty for top-level
    Children []string     // child work unit IDs
    Line     int          // source line in plan file
}

type UnitStatus int
const (
    UnitPending UnitStatus = iota
    UnitActive
    UnitComplete
    UnitArchived
)

type Plan struct {
    File   string
    Units  map[string]*WorkUnit // keyed by ID
    Root   []string             // top-level unit IDs in order
    Schema PlanSchema           // the parsed JSON config
}
```

### B.3 — Plan parser ✅

Reads the plan markdown file + schema config, produces a `Plan`:

1. Read schema JSON, validate
2. Read plan markdown file
3. Walk lines, matching markers at each level:
   - `heading`: match `depth` count of `#` + optional `prefix`
   - `checklist`: match `- [ ]` or `- [x]` (extract status)
   - `numbered`: match `N.` patterns
   - `bullet`: match `- ` at given indent depth
4. Build parent-child relationships based on level ordering
5. Derive IDs from hierarchy: `phase-1`, `phase-1/step-1.1`, etc.
6. Extract status from markers: `[x]` = Complete, `COMPLETE` in heading = Complete

### B.4 — Plan persistence in SQLite ✅

```sql
CREATE TABLE IF NOT EXISTS work_units (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    plan_file TEXT NOT NULL,
    name TEXT NOT NULL,
    level TEXT NOT NULL,
    status INTEGER DEFAULT 0,
    parent_id TEXT,
    line_number INTEGER,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id)
);

-- Link messages to work units
ALTER TABLE messages ADD COLUMN work_unit_id TEXT;
```

### Testing

- Parse `CRUSH_PATTERNS_PLAN.md` with the schema → correct unit tree
- Parse a differently-structured plan → correct unit tree
- Status extraction from `[x]`, `COMPLETE`, `[ ]` markers
- Round-trip: parse → store in SQLite → load → matches original

**Files:** `internal/plan/schema.go`, `internal/plan/parser.go`,
`internal/plan/plan.go`, `internal/storage/sqlite.go`

---

## Phase C: Work Unit Message Tracking ✅ COMPLETE

**Goal:** Tag messages with their work unit ID as work progresses through
a plan.

### C.1 — Active work unit tracking ✅

The engine tracks which work unit is currently active. When the user
starts working on a step (or the system detects it from message content),
new messages get tagged with that `work_unit_id`.

### C.2 — Manual work unit selection ✅

TUI command or key binding to set the active work unit:
- Show the plan tree in an overlay (`ctrl+p`)
- User selects a step → it becomes active
- All subsequent messages get that `work_unit_id`
- Slash commands: `/plan <schema>` to load, `/plan refresh`, `/plan` for status

### C.3 — Automatic step advancement ✅

When a checklist item is marked `[x]` in the plan file (detected on next
parse), the corresponding work unit status updates to Complete.

### Testing

- Set active work unit → messages tagged correctly
- Complete a step → status updates in SQLite
- Messages queryable by work unit: `WHERE work_unit_id = ?`

**Files:** `pkg/nostop/nostop.go`, `pkg/nostop/plantracker.go` (new),
`internal/tui/app.go`, `internal/tui/chat.go`,
`internal/tui/plan_overlay.go` (new)

---

## Phase D: Plan-Driven Archiving ✅ COMPLETE

**Goal:** When a work unit completes, archive its detail and retain a
summary.

**Completed:** 2025-01-XX. All items verified.

### D.1 — Work unit archive flow ✅

Reuses Phase A's summary-on-archive, but scoped to work unit:

1. Work unit marked complete
2. Query messages: `WHERE work_unit_id = ? AND is_archived = FALSE`
3. Call `BuildWorkUnitSummary()` with those messages
4. Insert summary message with `summary_source = 'work_unit'`
5. Mark detail messages `is_archived = TRUE`
6. Update work unit status to `Archived`

**Implementation:**
- `Archiver.ArchiveWorkUnit()` in `pkg/nostop/archiver.go`
- `SQLite.ArchiveWorkUnitWithSummary()` in `internal/storage/sqlite_workunit.go`
- `BuildWorkUnitSummary()` and `FormatWorkUnitSummary()` in `pkg/nostop/archiver.go`

### D.2 — Plan progress view ✅

TUI overlay showing the plan tree with:
- Status indicators (pending/active/complete/archived) ✅
- Token counts per work unit (cached, displayed for selected/archived) ✅
- Summary preview for archived units (`s` key) ✅
- Keys: `enter` to set active, `x` to complete, `a` to archive, `u` to restore ✅

**Implementation:**
- `PlanOverlay` in `internal/tui/plan_overlay.go` with new methods:
  - `archiveWorkUnit()`, `restoreWorkUnit()`, `loadSummaryPreview()`
  - `getWorkUnitStats()` with caching via `statsCache`
  - New messages: `WorkUnitArchivedMsg`, `WorkUnitRestoredMsg`

### D.3 — Work unit restore ✅

Like topic restore — re-expand a work unit's archived messages back into
active context. The summary message can optionally be kept or removed.

**Implementation:**
- `Archiver.RestoreWorkUnit()` in `pkg/nostop/archiver.go`
- `SQLite.RestoreWorkUnit()` in `internal/storage/sqlite_workunit.go`
- `keepSummary` parameter controls whether summary is preserved

### Testing

- Complete step → archive → summary created → detail gone from active
- Active context token count reduced by archived tokens
- Summary appears in chat view
- Restore work unit → detail messages reactivated
- Full cycle: parse plan → work through steps → archive each → context
  stays bounded → plan completes

**Files:** `pkg/nostop/archiver.go`, `internal/tui/plan_overlay.go`,
`internal/storage/sqlite.go`, `internal/storage/sqlite_workunit.go` (new)

---

## Execution Order

```
Phase A (summary-on-archive)     ← enhances existing topic archiving
    ↓
Phase B (schema + parser)        ← standalone, no TUI dependency
    ↓
Phase C (message tracking)       ← depends on B for work unit IDs
    ↓
Phase D (plan-driven archiving)  ← depends on A + B + C
```

Phase A and Phase B can run in parallel — they don't depend on each other.

---

## Risk Assessment

| Risk | Impact | Mitigation |
|------|--------|------------|
| Summary too lossy | Agent lacks detail for follow-up | Include file paths + action verbs; allow restore |
| Plan parsing fragile | Non-standard markdown breaks parser | Strict marker matching; clear error messages; fallback to manual unit definition |
| work_unit_id migration | Existing messages have NULL | NULL is fine — only new messages in plan-driven sessions get tagged |
| Two archiving modes confusing | User unsure which is active | Plan mode is explicit (requires schema file); topic mode is default |
| Summary token cost | Summaries accumulate | Cap at ~200 tokens per summary; oldest summaries archivable too |
