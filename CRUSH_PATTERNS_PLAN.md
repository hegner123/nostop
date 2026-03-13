# Crush Patterns & Charm Packages Adoption Plan

> Adopt proven architectural patterns from charmbracelet/crush and upgrade to
> the Charm v2 ecosystem. Focus on streaming reliability, UI performance, and
> concurrency safety.

## Status: Draft v2

## Context

Crush (charmbracelet/crush) is a mature Go TUI agentic coding tool built on
the Charm ecosystem. After reviewing their source, several patterns directly
address weaknesses in nostop's current architecture:

| Current nostop weakness | Crush solution |
|---|---|
| Global `var streamChan` for stream events | Generic `Broker[T]` pubsub with backpressure |
| Flat string message content | Interface-based `ContentPart` polymorphism |
| Full viewport re-render on each delta | Render cache per message item, invalidate only active |
| No concurrency protection on shared messages | Clone-on-publish before broadcasting |
| Viewport-based chat (no per-item control) | List-based chat with independent item rendering |
| Basic spinner during streaming | Gradient animation with visibility optimization |
| Bubbletea v1 + lipgloss v1 | Bubbletea v2 + lipgloss v2 + ultraviolet layout |

---

## Phase 1: Charm v2 Upgrade

**Goal:** Upgrade from Bubbletea v1 / lipgloss v1 to v2. This is the
foundation — every subsequent phase builds on v2 APIs.

**Rationale for doing this first:** The v2 upgrade touches every TUI file.
Doing it before any other TUI modifications avoids merge conflicts and means
all subsequent phases are written against the v2 API from the start.

### 1.1 — Dependency changes

```
# Remove
github.com/charmbracelet/bubbletea  v1.3.10
github.com/charmbracelet/lipgloss   v1.1.0
github.com/charmbracelet/bubbles    v0.21.0

# Add
charm.land/bubbletea/v2
charm.land/lipgloss/v2
charm.land/bubbles/v2
```

Install:
```sh
go get charm.land/bubbletea/v2@latest
go get charm.land/bubbles/v2@latest
go get charm.land/lipgloss/v2@latest
```

Note: `charm.land` vanity domain paths are confirmed correct — verified
against official upgrade guides and pkg.go.dev.

### 1.2 — Breaking changes checklist

**View() signature change** (biggest change):
```go
// v1
func (m Model) View() string { return "..." }

// v2
func (m Model) View() tea.View { return tea.NewView("...") }
```

All program options move to `tea.View` fields:
- `tea.WithAltScreen()` -> `view.AltScreen = true`
- `tea.WithMouseCellMotion()` -> `view.MouseMode = tea.MouseModeCellMotion`
- `tea.WithReportFocus()` -> `view.ReportFocus = true`

**Key messages:**
- `tea.KeyMsg` (struct) -> `tea.KeyPressMsg` (concrete type under `tea.KeyMsg` interface)
- `msg.Type` -> `msg.Code` (rune)
- `msg.Runes` -> `msg.Text` (string)
- `msg.Alt` -> `msg.Mod.Contains(tea.ModAlt)`
- `case " ":` -> `case "space":`
- `tea.KeyCtrlC` -> `msg.String() == "ctrl+c"`

**Mouse messages:**
- `tea.MouseMsg` (struct) -> interface, split into:
  - `tea.MouseClickMsg`
  - `tea.MouseMotionMsg`
  - `tea.MouseReleaseMsg`
  - `tea.MouseWheelMsg`
- `msg.X, msg.Y` -> `msg.Mouse().X, msg.Mouse().Y`
- `tea.MouseButtonLeft` -> `tea.MouseLeft`

**Paste messages:**
- `tea.KeyMsg` with `msg.Paste` -> `tea.PasteMsg` with `msg.Content`

**Bubbles v2:**
- Import paths: `charm.land/bubbles/v2/{textarea,viewport,spinner,help,key}`
- `runeutil` package removed (now internal)
- Textarea, viewport, spinner constructors may have minor changes

**Removed commands** (now View fields):
- `tea.EnterAltScreen` / `tea.ExitAltScreen` -> `view.AltScreen`
- `tea.EnableMouseCellMotion` -> `view.MouseMode`
- `tea.SetWindowTitle(s)` -> `view.WindowTitle = s`

### 1.3 — Migration strategy

1. Create branch `charm-v2-upgrade`
2. Update `go.mod` — replace all three packages at once (required: they
   depend on each other)
3. Fix import paths across all files
4. Fix `View()` return types in all models (`app.go`, `chat.go`, `history.go`,
   `topics.go`, `debug.go`, `restore.go`, `log.go`)
5. Fix key handling in all `Update()` methods
6. Fix mouse handling (if any)
7. Move program options to View fields
8. Run `go vet ./...` and `go build ./...`
9. Run `go fmt` on all modified files
10. Manual test: verify each of the 4 views renders and responds to input

### Testing

- `go build ./...` compiles without errors
- `go vet ./...` passes
- Manual: chat view streams correctly
- Manual: history view lists and selects conversations
- Manual: topics view shows active/archived topics
- Manual: debug view renders context usage
- Manual: keyboard shortcuts work (ctrl+n, ctrl+h, ctrl+t, ctrl+d, tab)

**Estimated files:** All files in `internal/tui/` (8), `cmd/nostop/main.go`,
`go.mod`, `go.sum`

---

## Phase 2: Message Content Model Refactor

**Goal:** Replace flat string message content with a polymorphic parts-based
model that cleanly handles text, thinking, tool calls, tool results, and
finish reasons as independent content parts.

### 2.1 — ContentPart interface

**New package:** `internal/message/`

```go
// internal/message/content.go

type ContentPart interface {
    isPart()  // sealed to this package
}

type TextContent struct {
    Text string `json:"text"`
}

type ThinkingContent struct {
    Thinking   string `json:"thinking"`
    Signature  string `json:"signature"`
    StartedAt  int64  `json:"started_at,omitempty"`
    FinishedAt int64  `json:"finished_at,omitempty"`
}

type ToolCall struct {
    ID       string `json:"id"`
    Name     string `json:"name"`
    Input    string `json:"input"`   // JSON string (matches Claude API format)
    Finished bool   `json:"finished"`
}

type ToolResult struct {
    ToolCallID string `json:"tool_call_id"`
    Name       string `json:"name"`
    Content    string `json:"content"`
    IsError    bool   `json:"is_error"`
}

type Finish struct {
    Reason  FinishReason `json:"reason"`
    Time    int64        `json:"time"`
    Message string       `json:"message,omitempty"`
    Details string       `json:"details,omitempty"`
}
```

Note: `ToolCall.Input` is stored as a JSON string (not `map[string]any`)
because that's what the Claude API sends in streaming deltas — appending
string fragments is the natural operation during streaming. Parse to
structured form only at the UI rendering boundary.

### 2.2 — Message struct with Parts

```go
// internal/message/message.go

type MessageRole string
const (
    User      MessageRole = "user"
    Assistant MessageRole = "assistant"
    Tool      MessageRole = "tool"
)

type Message struct {
    ID        string
    Role      MessageRole
    TopicID   string
    Parts     []ContentPart
    Model     string
    CreatedAt int64
    UpdatedAt int64
}
```

Mutation methods:
- `AppendContent(delta string)` — find or create TextContent, append
- `AppendThinking(delta string)` — find or create ThinkingContent, append
- `FinishThinking()` — set FinishedAt timestamp
- `ThinkingDuration() time.Duration`
- `AddToolCall(tc ToolCall)` — upsert by ID
- `AppendToolCallInput(id, delta string)` — stream tool input JSON
- `FinishToolCall(id string)` — mark finished
- `AddToolResult(tr ToolResult)` — append result part
- `AddFinish(reason FinishReason, msg, details string)` — terminal state
- `Clone() Message` — deep copy (copies Parts slice)
- `Content() TextContent` — find first text part
- `IsFinished() bool` — check for Finish part
- `ToolCalls() []ToolCall` — extract all tool call parts

### 2.3 — JSON serialization with type wrapper

```go
type partType string
const (
    textType      partType = "text"
    thinkingType  partType = "thinking"
    toolCallType  partType = "tool_call"
    toolResultType partType = "tool_result"
    finishType    partType = "finish"
)

type partWrapper struct {
    Type partType        `json:"type"`
    Data json.RawMessage `json:"data"`
}

func marshalParts(parts []ContentPart) ([]byte, error)
func unmarshalParts(data []byte) ([]ContentPart, error)
```

### 2.4 — SQLite migration

**Migration SQL** (added to `Migrations` slice in `schema.go`):

```sql
-- Migration 1: Add parts column to messages table
ALTER TABLE messages ADD COLUMN parts TEXT;

-- Migrate existing content to parts format
UPDATE messages SET parts = json_array(
    json_object('type', 'text', 'data', json_object('text', content))
) WHERE parts IS NULL AND content IS NOT NULL;

-- Add parts column to message_archive table
ALTER TABLE message_archive ADD COLUMN parts TEXT;

-- Migrate existing archive content
UPDATE message_archive SET parts = json_array(
    json_object('type', 'text', 'data', json_object('text', full_content))
) WHERE parts IS NULL AND full_content IS NOT NULL;
```

**Read strategy:** Read from `parts` if non-NULL, else construct a
single-element `[TextContent{Text: content}]` from the `content` column.
This provides backward compatibility without requiring all rows to be
migrated before the code ships.

**Write strategy:** Always write to both `parts` (new format) and `content`
(legacy, extract text parts only) until a future version drops `content`.

### Testing

- Unit test: `marshalParts` / `unmarshalParts` round-trip for each part type
- Unit test: Message mutation methods (AppendContent, AddToolCall, etc.)
- Unit test: Clone produces independent copy (mutate original, verify clone unchanged)
- Integration test: write Message with parts to SQLite, read back, verify equality
- Integration test: read legacy row (parts NULL, content non-NULL), verify TextContent produced

**Estimated files:** 3 new (`internal/message/content.go`, `message.go`,
`content_test.go`), 4 modified (`schema.go`, `sqlite.go`, `models.go`,
`nostop.go`)

---

## Phase 3: Generic PubSub Broker

**Goal:** Replace the global `var streamChan chan tea.Msg` with a typed,
generic broker that supports multiple subscribers, non-blocking publish, and
clean shutdown.

**New package:** `internal/pubsub/`

### 3.1 — Broker[T] implementation

```go
// internal/pubsub/broker.go

const defaultBufferSize = 100  // match current streamChan buffer

type Broker[T any] struct {
    subs     map[chan Event[T]]struct{}
    mu       sync.RWMutex
    done     chan struct{}
}

func NewBroker[T any]() *Broker[T]
func (b *Broker[T]) Subscribe(ctx context.Context) <-chan Event[T]
func (b *Broker[T]) Publish(t EventType, payload T)
func (b *Broker[T]) Shutdown()
```

```go
// internal/pubsub/events.go

type EventType string
const (
    CreatedEvent EventType = "created"
    UpdatedEvent EventType = "updated"
    DeletedEvent EventType = "deleted"
)

type Event[T any] struct {
    Type    EventType
    Payload T
}

type Subscriber[T any] interface {
    Subscribe(context.Context) <-chan Event[T]
}
```

Design decisions:
- Buffer size 100 (matching current `streamChan` — do not reduce)
- Non-blocking publish: `select { case sub <- event: default: }` — if a
  subscriber's buffer is full, skip rather than block the publisher
- Context-based lifecycle: subscriber goroutine watches `ctx.Done()`, then
  removes itself from the subscriber map and closes its channel
- `Shutdown()` closes all subscriber channels for clean process exit

### 3.2 — Fan-in bridge to Bubbletea

```go
// internal/pubsub/bridge.go

func SetupBridge[T any](
    ctx context.Context,
    wg *sync.WaitGroup,
    name string,
    subscriber func(context.Context) <-chan Event[T],
    output chan<- tea.Msg,
)
```

Goroutine drains subscriber channel into the shared `output` channel.
**No send timeout** — the output channel is buffered (100) and if it fills,
the non-blocking publish in the broker already handles backpressure. Adding
a timeout on the bridge creates a second drop point with no diagnostic
value.

### 3.3 — Message service

```go
// internal/message/service.go

type Service interface {
    pubsub.Subscriber[Message]
    Create(ctx context.Context, topicID string, params CreateParams) (Message, error)
    Update(ctx context.Context, message Message) error
    Get(ctx context.Context, id string) (Message, error)
    List(ctx context.Context, topicID string) ([]Message, error)
    Delete(ctx context.Context, id string) error
}

type service struct {
    *pubsub.Broker[Message]
    storage *storage.SQLite
}
```

Every mutation (`Create`, `Update`, `Delete`) persists to SQLite then
publishes via the embedded broker. `Update` publishes `message.Clone()` to
prevent races.

### 3.4 — Migrate streaming flow

**Before:**
```
ChatModel.streamResponse() -> goroutine -> streamChan (global var) -> waitForStreamMsg
```

**After:**
```
Nostop.SendStream()
  -> mutates Message via mutation methods
  -> calls messageService.Update()
    -> persists to SQLite
    -> publishes Event[Message] via Broker
      -> fan-in bridge -> program.Send()
        -> UI Update() receives pubsub.Event[Message]
```

This eliminates:
- The global `var streamChan`
- The `waitForStreamMsg` polling command
- The `StreamStartMsg` / `StreamChunkMsg` / `StreamDoneMsg` / `StreamErrorMsg`
  types (replaced by `pubsub.Event[Message]` with the message's `Parts`
  indicating state)

### Testing

- Unit test: Broker subscribe/publish/shutdown lifecycle
- Unit test: Broker handles slow subscriber (non-blocking publish)
- Unit test: Broker context cancellation removes subscriber
- Unit test: Multiple subscribers each receive all events
- Integration test: publish Message event, verify received through bridge

**Estimated files:** 4 new (`broker.go`, `events.go`, `bridge.go`,
`service.go`), 4 modified (`chat.go`, `app.go`, `nostop.go`, `main.go`)

---

## Phase 4: List-Based Chat with Render Caching

**Goal:** Replace the current viewport-based chat with a list of independent
message items, each with its own render cache and animation state.

### 4.1 — Message items

```
internal/tui/chat/
  items.go       # MessageItem interface, cachedMessageItem, shared helpers
  user.go        # UserMessageItem
  assistant.go   # AssistantMessageItem (thinking, animation, cache)
  tool.go        # ToolMessageItem (status state machine, cache)
  info.go        # AssistantInfoItem (model name, response duration)
```

**Core interface:**
```go
type MessageItem interface {
    ID() string
    Render(width int) string
    RawRender(width int) string  // without border/padding, for caching
}
```

**Render caching (embedded in each item):**
```go
type cachedMessageItem struct {
    rendered string
    width    int
    height   int
}

func (c *cachedMessageItem) getCachedRender(width int) (string, int, bool)
func (c *cachedMessageItem) setCachedRender(rendered string, width, height int)
func (c *cachedMessageItem) clearCache()
```

Cache invalidation rules:
- `SetMessage()` / `SetToolCall()` / `SetResult()` -> `clearCache()`
- Width change (window resize) -> cache miss (different key)
- Color profile change -> `clearCache()` on all items

### 4.2 — Chat component

```go
type Chat struct {
    list          *list.List       // bubbles v2 list
    idIndexMap    map[string]int   // O(1) message lookup by ID
    pausedAnims   map[string]struct{} // off-screen animation IDs
    follow        bool             // auto-scroll to bottom
}
```

Methods:
- `SetMessages(items...)` — full replace (session load/switch)
- `AppendMessages(items...)` — incremental (streaming)
- `RemoveMessage(id string)` — by ID
- `MessageItem(id string) MessageItem` — O(1) lookup
- `Animate(msg anim.StepMsg) tea.Cmd` — only propagate to visible items
- `RestartPausedVisibleAnimations() tea.Cmd` — on scroll
- `ScrollToBottom()` / `ScrollBy(lines int)` — with follow mode tracking

### 4.3 — Animation system

```
internal/tui/anim/
  anim.go     # Gradient spinner with configurable colors + label
  step.go     # StepMsg type for animation ticks
```

Gradient animation cycling between two colors, with label
("Thinking", "Generating"). Visibility-aware: pause off-screen items,
restart when scrolled back into view.

### 4.4 — Wire streaming events to items

In the main `Update()`:
```go
case pubsub.Event[message.Message]:
    switch msg.Type {
    case pubsub.CreatedEvent:
        m.appendSessionMessage(msg.Payload)
    case pubsub.UpdatedEvent:
        m.updateSessionMessage(msg.Payload)
    case pubsub.DeletedEvent:
        m.chat.RemoveMessage(msg.Payload.ID)
    }
```

`updateSessionMessage()`:
1. Find `AssistantMessageItem` by ID in `idIndexMap` — O(1)
2. Call `item.SetMessage(&msg)` — clears only that item's cache
3. For each new `ToolCall` in `msg.Parts`, create `ToolMessageItem`
4. For each updated `ToolCall`, call `item.SetToolCall(tc)`
5. If `chat.follow`, scroll to bottom

### Testing

- Unit test: cachedMessageItem hit/miss/invalidation
- Unit test: Chat.idIndexMap consistency after append/remove
- Unit test: AssistantMessageItem renders thinking + text + error states
- Unit test: ToolMessageItem status transitions
- Manual: streaming renders smoothly with 50+ messages in history
- Manual: scroll up pauses follow, scroll to bottom resumes

**Estimated files:** 7 new, 4 modified

---

## Phase 5: Clone-on-Publish & Concurrency Safety

**Goal:** Ensure no races between the streaming goroutine mutating messages
and the UI goroutine reading them.

### 5.1 — Clone before every Publish

In the message service:
```go
func (s *service) Update(ctx context.Context, msg Message) error {
    // ... persist to SQLite ...
    s.Publish(pubsub.UpdatedEvent, msg.Clone())
    return nil
}
```

`Clone()` copies the `Parts` slice. The interface values within are value
types (structs, not pointers), so a shallow slice copy is sufficient.

### 5.2 — Thread-safe value wrappers

**New package:** `internal/csync/`

```go
type Value[T any] struct {
    val T
    mu  sync.RWMutex
}
func (v *Value[T]) Get() T
func (v *Value[T]) Set(val T)

type Map[K comparable, V any] struct {
    m  map[K]V
    mu sync.RWMutex
}
func (m *Map[K,V]) Get(key K) (V, bool)
func (m *Map[K,V]) Set(key K, val V)
func (m *Map[K,V]) Del(key K)
```

Use for:
- Active request tracking (`Map[string, context.CancelFunc]`)
- Message queue for busy sessions (`Map[string, []QueuedCall]`)
- System prompt / model config that may change mid-session (`Value[string]`)

### 5.3 — Audit existing shared state

- `TopicTracker.topics` — already has `sync.RWMutex`, verify all access
  paths hold appropriate lock
- `Nostop` engine fields — already split-locked (`prepareStream`/
  `executeStream`/`finalizeStream`), verify completeness
- Stream callback closures — verify no captured mutable references leak
  across goroutine boundaries
- Run full test suite with `go test -race ./...`

### Testing

- `go test -race ./...` passes
- Unit test: `Value[T]` concurrent read/write
- Unit test: `Map[K,V]` concurrent operations
- Unit test: `Message.Clone()` produces independent copy

**Estimated files:** 4 new (`csync/*.go` + tests), 3 modified

---

## Phase 6: Enhanced Streaming UX

**Goal:** Rich streaming experience with thinking indicators, tool status
badges, and smooth transitions.

### 6.1 — Thinking box

When `ThinkingContent` is present but no `TextContent` yet:
- Render bordered box with thinking text (lipgloss border style)
- Collapse to last 10 lines by default
- Show "Thought for Ns" footer when `FinishedAt > 0`
- Click or space to toggle expand/collapse
- Clear cache on toggle

### 6.2 — Tool call status indicators

State machine per `ToolMessageItem`:
```
Pending -> Running -> Success | Error | Canceled
```

Visual rendering:
- **Pending**: gradient spinner + tool name (input not yet received)
- **Running**: gradient spinner + tool name + parsed params summary
- **Success**: checkmark icon + tool name + collapsed output (10 lines max)
- **Error**: X icon + tool name + error message
- **Canceled**: italic "Canceled" text

Collapsible output body: click/space to expand full tool output.

### 6.3 — Follow mode

- Auto-scroll to bottom during streaming (when user hasn't scrolled up)
- Disable follow on any upward scroll
- Re-enable follow on explicit scroll-to-bottom or new user message send
- Visual indicator when not following: "New content below" at bottom edge

### 6.4 — Native progress bar

Bubbletea v2 supports native terminal progress bars as a View field:
```go
func (m Model) View() tea.View {
    v := tea.NewView(content)
    if m.isStreaming {
        v.ProgressBar = tea.IndeterminateProgressBar
    }
    return v
}
```

Works in Ghostty, Windows Terminal, and other supporting terminals.

### Testing

- Manual: thinking box renders during extended thinking models
- Manual: tool calls show spinner while running, checkmark on complete
- Manual: follow mode disengages on scroll up, re-engages on scroll down
- Manual: progress bar visible in supported terminals

**Estimated files:** 6 modified

---

## Phase 7: Ultraviolet Layout System

**Goal:** Replace manual coordinate math with ultraviolet's declarative
screen-based layout.

**Deferred until ultraviolet API stabilizes.** This phase is planned but not
committed. It will be evaluated after Phases 0-6 are complete. If ultraviolet
reaches a stable release, adopt it. Otherwise, the manual layout from Phase 1
is sufficient.

### 7.1 — Screen-based rendering

```go
import uv "github.com/charmbracelet/ultraviolet"

func (m *UI) View() tea.View {
    scr := uv.NewScreen(m.width, m.height)
    m.header.Draw(scr, m.layout.header)
    m.chat.Draw(scr, m.layout.main)
    m.sidebar.Draw(scr, m.layout.sidebar)
    m.editor.Draw(scr, m.layout.editor)
    return tea.NewView(scr.String())
}
```

### 7.2 — Layout calculation

```go
type uiLayout struct {
    header  uv.Rectangle
    main    uv.Rectangle
    sidebar uv.Rectangle
    editor  uv.Rectangle
}
```

Recalculated on `tea.WindowSizeMsg` with breakpoints for compact mode.

**Estimated files:** 3 new, 2 modified (when/if executed)

---

## Dependency Summary

### New packages to add

| Package | Import Path | Required by |
|---|---|---|
| Bubbletea v2 | `charm.land/bubbletea/v2` | Phase 1 |
| Lipgloss v2 | `charm.land/lipgloss/v2` | Phase 1 |
| Bubbles v2 | `charm.land/bubbles/v2` | Phase 1 |
| Charmbracelet x | `github.com/charmbracelet/x` | Phase 1 (transitive) |
| Ultraviolet | `github.com/charmbracelet/ultraviolet` | Phase 7 (deferred) |

### Packages NOT adopting

| Package | Reason |
|---|---|
| `charm.land/fantasy` | Multi-provider LLM abstraction — we only target Claude API |
| `charm.land/catwalk` | Model catalog — we have our own config |
| `posthog-go` | Telemetry — not applicable |

---

## Execution Order

```
Phase 1 (Charm v2 Upgrade)
  |
  v
Phase 2 (Content Model)
  |
  v
Phase 3 (PubSub Broker)
  |
  v
Phase 4 (List Chat + Caching)
  |
  v
Phase 5 (Clone + Concurrency)
  |
  v
Phase 6 (Streaming UX)
  |
  v
Phase 7 (Ultraviolet — deferred)
```

**Strictly sequential.** Each phase merges to main before the next begins.
This avoids merge conflicts between phases that touch the same files and
ensures each phase builds on a stable, tested foundation.

Note: `SendStream` mutex contention (identified in review) is already fixed —
`nostop.go` splits locking into `prepareStream` (write lock), `executeStream`
(no lock), and `finalizeStream` (write lock).

---

## Risk Assessment

| Risk | Mitigation |
|---|---|
| Bubbletea v2 migration breaks TUI | Phase 1 on branch, manual test all 4 views before merge |
| View() return type change cascades | Follow official UPGRADE_GUIDE_V2.md checklist item by item |
| Render caching serves stale data | Conservative: clear on any `Set*()` call + color profile change |
| PubSub event drops during fast streaming | Buffer size 100 (not 64), non-blocking publish in broker only |
| SQLite migration corrupts data | Additive columns only, backward-compatible reads, both columns written |
| `message_archive` format mismatch | Migration covers both `messages` and `message_archive` tables |
| Ultraviolet API unstable | Phase 7 deferred, not blocking |
| `-race` detector finds issues | Phase 5 explicitly runs race detector, fix before proceeding |
