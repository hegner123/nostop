# AGENTS.md

Guide for AI agents working in this codebase.

## Project Overview

**nostop** — A Go CLI and library for intelligent topic-based context management when chatting with Claude. Instead of naively truncating conversation history, nostop detects topic shifts via a sidecar model (Haiku), scores relevance, and archives low-relevance topics to SQLite while preserving full message content for later restoration.

- **Language**: Go 1.24+
- **Module**: `github.com/hegner123/nostop`
- **Binary**: `nostop` (entrypoint: `cmd/nostop/main.go`)
- **Build tool**: [just](https://github.com/casey/just) (justfile)
- **TUI framework**: [Bubbletea](https://github.com/charmbracelet/bubbletea) + [Lipgloss](https://github.com/charmbracelet/lipgloss)
- **Database**: SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **Config format**: TOML via `github.com/BurntSushi/toml`

## Commands

| Command | Description |
|---------|-------------|
| `just build` | Build binary with ldflags (version, commit, time) + codesign |
| `just test` | Run all tests (`go test -v ./...`) |
| `just test-race` | Run tests with race detector |
| `just test-coverage` | Tests + coverage report (HTML) |
| `just bench` | Run benchmarks |
| `just fmt` | Format code (`gofmt -s -w .`) |
| `just vet` | Run `go vet ./...` |
| `just lint` | Format + vet |
| `just check` | Format + vet + test |
| `just ci` | Full CI: deps + lint + test-race + build |
| `just run` | Build and run the TUI |
| `just dev` | Build with race detector and run |
| `just tidy` | `go mod tidy` |
| `just clean` | Remove build artifacts |
| `just install` | Build and install to `/usr/local/bin` |

**Quick validation after changes**: `just check`

## Project Structure

```
cmd/nostop/main.go          # CLI entrypoint, flag parsing, config loading, TUI launch
pkg/nostop/                  # Public library API (importable by external consumers)
  nostop.go                  # Nostop engine: main orchestrator (Send, SendStream, BuildContext)
  config.go                  # TOML config loading, validation, env overrides
  context.go                 # ContextManager: token counting, usage tracking
  cache.go                   # CacheStrategy: Anthropic prompt caching (5m/1h TTLs)
  archiver.go                # Archiver: topic archival/restoration logic
  errors.go                  # Sentinel errors, NostopError type, error categorization
internal/
  api/                       # Claude Messages API client
    client.go                # HTTP client, Send, CountTokens
    stream.go                # SSE streaming (StreamReader, StreamWithCallback)
    retry.go                 # Retry logic with exponential backoff + jitter
    types.go                 # Request/Response types, model constants, error types
  storage/                   # SQLite persistence layer
    sqlite.go                # CRUD operations for conversations, topics, messages, archives
    models.go                # Data models (Conversation, Topic, Message, ArchiveEvent)
    schema.go                # DDL schema definitions
    errors.go                # Storage-specific sentinel errors
  topic/                     # Topic detection and tracking
    detector.go              # TopicDetector: uses Haiku to detect topic shifts
    scorer.go                # TopicScorer: relevance scoring via Haiku
    tracker.go               # TopicTracker: topic state, context allocation algorithm
  tui/                       # Bubbletea TUI views
    app.go                   # Main App model, view routing, global keybindings
    chat.go                  # Chat view (message display, input, streaming)
    history.go               # Conversation history browser
    topics.go                # Topics overview (active/archived)
    debug.go                 # Debug view (context usage, archive events)
    styles.go                # Lipgloss style definitions
    log.go                   # Debug file logger
    restore.go               # Topic restoration from chat context
```

## Architecture

### Core Flow (`Nostop.Send`)

1. Load conversation and topics from SQLite
2. Get or create current topic (uses Haiku for identification)
3. Store user message
4. Detect topic shift (async Haiku call — non-critical, failures logged but don't block)
5. Handle topic shift: create new topic, re-score old topics in background goroutine
6. Check context usage → if ≥95%, archive topics until ≤50%
7. Build context from active (non-archived) messages
8. Send to Claude (with optional prompt caching)
9. Store assistant response, update token counts

### Key Design Decisions

- **Archive, don't compact**: Full messages are preserved verbatim in `message_archive` table. Nothing is summarized or lost.
- **Topic detection is non-critical**: If Haiku calls fail, the system continues with the current topic. Never fail a user request because topic detection broke.
- **Background re-scoring**: When a topic shift is detected, relevance re-scoring of old topics happens in a `goroutine` tracked by `sync.WaitGroup` on the `Nostop` struct. The `Close()` method waits for these to finish before closing storage.
- **Concurrency**: The `Nostop` struct uses `sync.RWMutex` (`r.mu`). `Send`/`SendStream`/write operations take a write lock. Read operations (`GetTopics`, `ListConversations`, etc.) take a read lock.
- **Pure Go SQLite**: Uses `modernc.org/sqlite` — no CGO required. SQLite is configured with WAL mode, foreign keys, and a 5-second busy timeout.

### Context Allocation Formula

```
topic_allocation = base_weight × relevance_score × recency_factor
```

- `base_weight`: 1.0 for current topic, 0.5 for others
- `relevance_score`: 0.0–1.0 (scored by Haiku)
- `recency_factor`: `1.0 - (hours_since_active / 24)`, minimum 0.1
- 10% of context budget reserved for system prompt overhead

## Code Conventions

### Error Handling

- **Sentinel errors** in `pkg/nostop/errors.go` and `internal/storage/errors.go` — use `errors.Is()` for comparison
- **Wrapped errors**: Always wrap with `fmt.Errorf("context: %w", err)` to preserve error chains
- **`NostopError` type**: Structured error with operation name, retry info, recoverability flag, and user-friendly message
- **API errors**: `api.APIError` has typed error details (`ErrorType`). Use `api.ExtractAPIError()` to unwrap from chains.
- **Graceful degradation**: Non-critical operations (topic detection, scoring) log errors and continue rather than failing the request

### Naming

- **Exported types**: PascalCase (`Nostop`, `Config`, `TopicTracker`, `StreamReader`)
- **Constructors**: `NewXxx()` pattern (`NewClient`, `NewSQLite`, `NewTopicTracker`)
- **Config defaults**: `DefaultXxx()` functions (`DefaultConfig`, `DefaultRetryConfig`, `DefaultFileConfig`)
- **Functional options**: `WithXxx()` pattern on the API client (`WithBaseURL`, `WithRetryConfig`, `WithDebug`)
- **Storage methods**: CRUD pattern — `Create*`, `Get*`, `Update*`, `Delete*`, `List*`
- **Package-level doc comments** on every package declaration

### Patterns

- **Type aliases for public API**: `type Conversation = storage.Conversation` in `pkg/nostop/nostop.go` to expose internal types without leaking package paths
- **JSON tags**: All model structs have `json:"snake_case"` tags. Optional fields use `omitempty`.
- **Context passing**: All storage and API operations take `context.Context` as the first argument
- **Pointer receivers**: All methods on mutable structs use pointer receivers. Bubbletea `Update` methods follow Bubbletea conventions (value receivers on `App`, pointer sub-models).
- **Constants grouped by type**: Model names, error types, stream event types, etc. are grouped `const` blocks with typed string constants

### TUI (Bubbletea)

- **Four views**: Chat, History, Topics, Debug — routed via `View` enum in `app.go`
- **Message types**: Custom `tea.Msg` types for async operations (e.g., `StreamStartMsg`, `ConversationCreatedMsg`, `TopicRestoredMsg`)
- **Sub-models**: Each view has its own model struct (`ChatModel`, `DebugModel`, `TopicsModel`, `HistoryModel`) with `Init()`, `Update()`, `View()` methods
- **Global keybindings**: `ctrl+c` always quits (never blocked). `tab`/`shift+tab` cycle views. `ctrl+n/h/t/d` switch to specific views.
- **Debug logging**: `tui.Log()` writes to file when `--debug` flag is set. Uses `tui.InitLogger()` / `tui.CloseLogger()`.

## Testing

### Approach

- **Standard `testing` package** — no third-party test frameworks
- **Table-driven tests** for systematic error type/status code coverage (see `retry_test.go`, `sqlite_test.go`)
- **Test helpers**: `t.Helper()` on setup functions. Test DBs use `t.TempDir()` or `os.CreateTemp()`.
- **SQLite tests**: Create real temp-file databases (not in-memory) to test actual persistence. Cleaned up via `t.Cleanup()`.
- **No mocks for storage**: Tests use real SQLite instances with the full schema

### Test Files

| File | What it tests |
|------|---------------|
| `internal/api/retry_test.go` | Retry logic, backoff, error classification, error helpers |
| `internal/api/stream_test.go` | SSE parsing, stream collection |
| `internal/storage/sqlite_test.go` | Full CRUD, archival/restore, cascade deletes, statistics |
| `internal/topic/tracker_test.go` | Topic tracking, allocation algorithm, archival selection |
| `pkg/nostop/cache_test.go` | Cache strategy, prompt caching |
| `pkg/nostop/config_test.go` | Config loading, validation, env overrides |
| `pkg/nostop/context_test.go` | Context usage calculation |
| `pkg/nostop/errors_test.go` | Error categorization, recovery suggestions |

### Running Tests

```bash
just test          # All tests, verbose
just test-race     # With race detector (CI uses this)
go test ./internal/storage/...   # Single package
go test -run TestArchiveAndRestoreTopic ./internal/storage/...  # Single test
```

## Configuration

Config is loaded from TOML files (search order: `./nostop.toml` → `~/.config/nostop/config.toml` → `~/.nostop.toml`).

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `ANTHROPIC_API_KEY` | API key (overrides config file `api.key`) |
| `NOSTOP_DB_PATH` | Database path (overrides config file `database.path`) |
| `XDG_CONFIG_HOME` | Custom config directory |

### Key Thresholds

| Constant | Default | Location |
|----------|---------|----------|
| Archive threshold | 0.95 (95%) | `pkg/nostop/context.go` |
| Archive target | 0.50 (50%) | `pkg/nostop/context.go` |
| Max context tokens | 200,000 | `pkg/nostop/nostop.go` |
| Max response tokens | 8,192 | `pkg/nostop/nostop.go` (hardcoded in `Send`) |
| Retry max attempts | 3 | `internal/api/retry.go` |
| Retry initial backoff | 1s | `internal/api/retry.go` |
| System overhead reserve | 10% | `internal/topic/tracker.go` |

## CI

GitHub Actions workflow (`.github/workflows/test.yml`):
- Runs on push/PR to `main`
- Go 1.24
- Steps: `go mod download` → `go vet ./...` → `go test -race ./...`

## Gotchas

1. **macOS codesign**: The justfile runs `codesign -s -` after building. This is an ad-hoc signature required for macOS. Cross-platform builds skip this.
2. **Build ldflags**: Version info is injected at build time via `-ldflags` into `main.buildVersion`, `main.buildCommit`, `main.buildTime`. Use `just build`, not bare `go build`, to get proper version stamps.
3. **Background goroutines in Nostop**: The `handleTopicShift` method spawns goroutines for re-scoring. Always call `Nostop.Close()` (which calls `r.wg.Wait()`) to avoid writing to a closed DB.
4. **Mutex ordering**: `Nostop.mu` is the outer lock. `TopicTracker.mu` is the inner lock. The background re-scoring goroutine acquires `r.mu` independently — it captures a snapshot of topics before the goroutine starts to avoid racing.
5. **SQLite not-found returns nil, not error**: Storage `Get*` methods return `(nil, nil)` when a record doesn't exist — check for nil result, not just error.
6. **Keywords stored as JSON**: `Topic.Keywords` is `[]string` in Go but stored as a JSON string in SQLite. Use `KeywordsJSON()` for writes and `SetKeywordsFromJSON()` for reads.
7. **Stream vs Send**: Use `Send()` for non-streaming, `SendStream()`/`StreamWithCallback()` for streaming. Setting `Stream=true` on a `Send()` call returns an error.
8. **No CGO needed**: SQLite is pure Go (`modernc.org/sqlite`). No C compiler required for builds.
