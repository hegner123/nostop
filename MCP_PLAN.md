# MCP Client Support Plan

> Add Model Context Protocol (MCP) client support to nostop, enabling
> connection to external MCP servers that provide tools, resources, and
> prompts. This lets nostop leverage the same ecosystem of MCP servers
> that Claude Code, Cursor, and other MCP-aware clients use.

## Status: Draft v3 (2026-03-13, post-three-phase-review)

---

## Context

MCP (Model Context Protocol) is a JSON-RPC 2.0 based protocol for
connecting AI clients to tool servers. An MCP server exposes:

- **Tools** — callable functions with JSON Schema inputs (same shape as
  Claude's tool_use)
- **Resources** — read-only data (files, DB rows, API responses) the
  client can fetch
- **Prompts** — reusable prompt templates

Two transports are supported:
- **stdio** — server runs as a subprocess, client communicates via
  stdin/stdout
- **HTTP (Streamable HTTP)** — server runs remotely, client sends
  JSON-RPC over HTTP with optional SSE streaming

nostop currently has a tool system with two dispatch paths:
1. **Builtin** — Go functions compiled into the binary
2. **Subprocess** — CLI tools invoked via `exec.Command`

MCP adds a third: **JSON-RPC** — tool calls routed to an MCP server
process or endpoint.

---

## Architecture

```
┌─────────────────────────────────────────────────┐
│                   Nostop Engine                   │
│                                                   │
│   ┌───────────┐                                   │
│   │  Registry  │ ← tools from all sources         │
│   └─────┬─────┘                                   │
│         │                                         │
│   ┌─────▼─────┐                                   │
│   │  Executor  │                                   │
│   └─────┬─────┘                                   │
│         │                                         │
│   ┌─────┼──────────────┬──────────────┐           │
│   │     │              │              │           │
│   ▼     ▼              ▼              ▼           │
│ Builtin  Subprocess   MCP Server    MCP Server    │
│ (Go fn)  (exec.Cmd)   (stdio)      (HTTP)        │
│                        │              │           │
│                   ┌────┴────┐   ┌────┴────┐      │
│                   │ stump   │   │ remote  │      │
│                   │ sig     │   │ server  │      │
│                   │ repfor  │   │         │      │
│                   └─────────┘   └─────────┘      │
└─────────────────────────────────────────────────┘
```

### Key Design Decisions

**1. MCP tools register into the existing Registry.**
Each MCP tool becomes a `ToolDef` with a new dispatch type. The registry
and `APITools()` don't change — they already produce `api.Tool` from
`ToolDef.Name/Description/InputSchema`. Only the executor needs a new
dispatch path.

**2. MCP servers are managed by a ServerManager.**
Each configured server is a long-lived connection (stdio subprocess or
HTTP client). The manager handles lifecycle: start, health check, restart,
shutdown.

**3. Configuration follows the `.mcp.json` convention.**
Same format Claude Code uses — users can share configs between tools.

---

## Phase 1: MCP Protocol Layer

**Goal:** Implement the JSON-RPC 2.0 transport for MCP communication.

### 1.1 — JSON-RPC types

New package: `internal/mcp/`

```go
// JSON-RPC 2.0 message types
// ID is `any` because the JSON-RPC spec allows string, number, or null.
// Real MCP servers use both integer and string IDs.
type Request struct {
    JSONRPC string         `json:"jsonrpc"`
    ID      any            `json:"id"`
    Method  string         `json:"method"`
    Params  map[string]any `json:"params,omitempty"`
}

type Response struct {
    JSONRPC string         `json:"jsonrpc"`
    ID      any            `json:"id"`
    Result  map[string]any `json:"result,omitempty"`
    Error   *RPCError      `json:"error,omitempty"`
}

type RPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}

type Notification struct {
    JSONRPC string         `json:"jsonrpc"`
    Method  string         `json:"method"`
    Params  map[string]any `json:"params,omitempty"`
}
```

### 1.2 — Transport interface

```go
type Transport interface {
    Send(ctx context.Context, req *Request) (*Response, error)
    Notify(ctx context.Context, notif *Notification) error
    Close() error
}
```

Two implementations:

**`StdioTransport`** — manages a subprocess, writes JSON-RPC to stdin,
reads from stdout, line-delimited. Goroutine structure:

1. **Main goroutine**: Owns stdin writer. `Send()` writes request,
   blocks on response channel.
2. **Reader goroutine**: Runs `bufio.Scanner` on stdout. Parses each
   line as JSON-RPC response, routes to pending request by ID via a
   `map[any]chan *Response` protected by mutex.
3. **Stderr goroutine**: Drains stderr via `bufio.Scanner`, writes
   each line to the debug log (`log.Printf`). Prevents pipe buffer
   fill (64KB on macOS) which would hang the server process.
4. **Process watcher goroutine**: Calls `cmd.Wait()`. On exit, closes
   stdout pipe (unblocks reader goroutine), sets transport to error
   state, and completes all pending request channels with an error.

Partial output handling: If the process exits mid-line, the stdout
pipe close causes `Scanner.Scan()` to return false. The reader
goroutine detects this and drains all pending requests with an error.

`Close()` shutdown order: (1) Kill the subprocess via
`cmd.Process.Kill()`. (2) The process watcher goroutine detects exit,
closes stdout, and drains all pending response channels with an error.
(3) The reader goroutine exits when stdout closes. (4) The stderr
goroutine exits when stderr closes (subprocess death). `Close()`
blocks until all three goroutines have exited (use a `sync.WaitGroup`).
`Send()` calls during or after `Close()` return immediately with an
error (check a `closed atomic.Bool` before writing to stdin).

**`HTTPTransport`** — sends JSON-RPC over HTTP POST. Each `Send()`
is a single HTTP request/response round-trip. SSE handling is deferred
to Phase 5 (resource subscriptions) — not needed for tool calls in
Phases 1-4.

### 1.3 — MCP client

Wraps a transport and implements the MCP protocol methods:

```go
type Client struct {
    transport Transport
    info      ServerInfo
    nextID    atomic.Int64 // generates integer IDs; accepts any ID type in responses
}

// MCP lifecycle
func (c *Client) Initialize(ctx context.Context) (*ServerInfo, error)
func (c *Client) Ping(ctx context.Context) error
func (c *Client) Shutdown(ctx context.Context) error

// Tool operations
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error)
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error)

// Resource operations (Phase 3)
func (c *Client) ListResources(ctx context.Context) ([]ResourceInfo, error)
func (c *Client) ReadResource(ctx context.Context, uri string) (*ResourceContent, error)

// Prompt operations (Phase 3)
func (c *Client) ListPrompts(ctx context.Context) ([]PromptInfo, error)
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]any) (*PromptContent, error)
```

### 1.4 — MCP types

```go
type ServerCapabilities struct {
    Tools     *struct{} `json:"tools,omitempty"`
    Resources *struct{} `json:"resources,omitempty"`
    Prompts   *struct{} `json:"prompts,omitempty"`
}

type ServerInfo struct {
    Name            string             `json:"name"`
    Version         string             `json:"version"`
    Capabilities    ServerCapabilities `json:"capabilities"`
    ProtocolVersion string             `json:"protocolVersion"`
}

type ToolInfo struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    InputSchema map[string]any `json:"inputSchema"`
}

type ToolResult struct {
    Content []ContentBlock `json:"content"`
    IsError bool           `json:"isError,omitempty"`
}

// Phase 1 only handles type: "text" content blocks. Non-text blocks
// (image, resource) are logged as warnings and their type field
// preserved for future handling in Phase 5. Do NOT add speculative fields.
type ContentBlock struct {
    Type string `json:"type"` // "text", "image", "resource"
    Text string `json:"text,omitempty"`
}
```

### Testing

- JSON-RPC request/response serialization round-trip
- StdioTransport with a mock subprocess (echo server)
- HTTPTransport with httptest server
- Client Initialize/ListTools/CallTool against mock

**Files:** `internal/mcp/jsonrpc.go`, `internal/mcp/transport.go`,
`internal/mcp/stdio.go`, `internal/mcp/http.go`, `internal/mcp/client.go`,
`internal/mcp/types.go`

---

## Phase 2: Server Management and Configuration

**Goal:** Configure, start, and manage MCP server connections.

### 2.1 — Configuration format

File: `.mcp.json` (project root) or `~/.config/nostop/mcp.json` (user).
Compatible with Claude Code's format:

```json
{
  "mcpServers": {
    "stump": {
      "command": "stump",
      "args": ["--mcp"],
      "env": {}
    },
    "sig": {
      "command": "sig",
      "args": ["--mcp"]
    },
    "remote-server": {
      "url": "https://mcp.example.com/v1",
      "headers": {
        "Authorization": "Bearer ${MCP_TOKEN}"
      }
    }
  }
}
```

**Fields per server:**

| Field | Type | Transport | Description |
|-------|------|-----------|-------------|
| `command` | string | stdio | Binary to execute |
| `args` | []string | stdio | Command arguments |
| `env` | map | stdio | Extra environment variables |
| `url` | string | HTTP | Server endpoint URL |
| `headers` | map | HTTP | HTTP headers (supports `${VAR}` expansion) |
| `disabled` | bool | both | Skip this server |
| `timeout` | string | both | Per-call timeout (default "30s") |

Presence of `command` → stdio transport. Presence of `url` → HTTP
transport. Both present → error.

### 2.2 — Server manager

```go
type ServerManager struct {
    servers map[string]*ManagedServer
    mu      sync.RWMutex
}

type ManagedServer struct {
    Name      string
    Config    ServerConfig
    Client    *Client
    Tools     []ToolInfo
    Status    ServerStatus // Starting, Ready, Error, Stopped
    Error     error
}

func NewServerManager() *ServerManager
func (m *ServerManager) LoadConfig(path string) error
func (m *ServerManager) StartAll(ctx context.Context) error
func (m *ServerManager) Start(ctx context.Context, name string) error
func (m *ServerManager) Stop(name string) error
func (m *ServerManager) StopAll() error
func (m *ServerManager) AllTools() []ToolInfo
func (m *ServerManager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*ToolResult, error)
func (m *ServerManager) Status() map[string]ServerStatus
```

### 2.3 — Config discovery

Search order (all files loaded, merged — if the same server name
appears in multiple files, the file with the lowest number wins):
1. `--mcp-config` CLI flag
2. `.mcp.json` in working directory
3. `.mcp.json` in project root (git root)
4. `~/.config/nostop/mcp.json`

Merge semantics: All config files are loaded. Server definitions are
merged across files. If the same server name appears in multiple files,
the definition from the higher-precedence file wins (e.g., project
`.mcp.json` overrides user config). This allows project-specific
servers plus global servers.

Environment variable expansion in config values: `${VAR}` and
`${VAR:-default}`.

`StartAll` starts servers concurrently using `errgroup.Group` to
minimize startup latency. Each server's Initialize handshake runs in
its own goroutine. Failures are collected but do not prevent other
servers from starting — failed servers are marked Error status.

### Testing

- Parse valid `.mcp.json` → correct server configs
- Env var expansion: `${HOME}` resolves, `${MISSING:-fallback}` uses default
- Start stdio server → Initialize handshake succeeds
- Start HTTP server → Initialize handshake succeeds
- Server crash → status becomes Error, tools removed from registry
- StopAll → all subprocess servers killed cleanly

**Files:** `internal/mcp/config.go`, `internal/mcp/manager.go`,
`internal/mcp/config_test.go`, `internal/mcp/manager_test.go`

---

## Phase 3: Registry Integration

**Goal:** MCP server tools appear in the tool registry alongside builtins
and subprocess tools, with unified execution.

### 3.0 — Registry thread safety

Add `sync.RWMutex` to `Registry`. This is required because MCP server
lifecycle events (connect, disconnect, crash) add and remove tools at
runtime, while `executeStream` reads from the registry concurrently
without holding the engine lock.

- `Get()`, `APITools()`, `Names()`, `Len()`: take read lock
- `Register()`, `Remove()`: take write lock
- `CheckBinaries()`: take read lock (iterates tools)

This is the standard Go pattern for concurrent map access and the most
commonly used approach in the stdlib and ecosystem.

### 3.1 — New dispatch type in ToolDef

Add `MCPServer` field to the existing `ToolDef` struct. The full
struct (showing all 9 existing fields plus the new one):

```go
type ToolDef struct {
    Name        string
    Description string
    InputSchema map[string]any
    Builtin     BuiltinFunc        // in-process Go function
    Binary      string             // subprocess CLI tool
    NeedsCLI    bool               // subprocess needs --cli flag
    FlagMap     map[string]FlagSpec // JSON param → CLI flag mapping
    StdinParam  string             // param to pipe via stdin
    Timeout     time.Duration      // per-tool timeout override

    // New field for MCP dispatch
    MCPServer   string             // MCP server name (dispatches via ServerManager)
}

func (d ToolDef) IsMCPTool() bool {
    return d.MCPServer != ""
}
```

Update `CheckBinaries()` to skip MCP tools: add
`if def.IsMCPTool() { continue }` alongside the existing
`if def.IsBuiltinTool() { continue }` check. Otherwise
`LookPath("")` returns an error for every MCP tool.

### 3.2 — Tool discovery and registration

On startup (or when a server connects):
1. `ServerManager.Start()` calls `client.ListTools()`
2. Each `ToolInfo` becomes a `ToolDef` with `MCPServer` set
3. `Registry.Register()` adds it with the three-segment namespaced key
   `mcp__<servername>__<toolname>` (e.g., `mcp__stump__stump`),
   matching Claude Code convention. The `MCPServer` field stores only
   the server name (e.g., `"stump"`), not the full prefix.
4. If a server disconnects, its tools are removed from the registry

### 3.3 — Executor dispatch

Add MCP path to `Executor.Execute()`:

```go
func (e *Executor) Execute(ctx context.Context, name string, input map[string]any) Result {
    def, ok := e.registry.Get(name)
    if !ok {
        return Result{IsError: true, Error: "unknown tool: " + name}
    }

    if def.IsBuiltinTool() {
        return e.executeBuiltin(ctx, def, input)
    }
    if def.IsMCPTool() {
        return e.executeMCP(ctx, def, input)
    }
    return e.executeSubprocess(ctx, def, input)
}

// Executor currently has 4 fields: registry, defaultTimeout, workDir,
// readTracker. Add mcpManager as a 5th field.
func (e *Executor) executeMCP(ctx context.Context, def ToolDef, input map[string]any) Result {
    // Strip namespace prefix to get the bare tool name the MCP server expects.
    // Registry name: "mcp__stump__stump" -> server expects: "stump"
    bareToolName := mcpBareToolName(def.Name)
    result, err := e.mcpManager.CallTool(ctx, def.MCPServer, bareToolName, input)
    if err != nil {
        return Result{IsError: true, Error: err.Error()}
    }
    return Result{Output: extractMCPText(result), IsError: result.IsError}
}

// mcpBareToolName strips the "mcp__<server>__" prefix from a namespaced tool name.
func mcpBareToolName(name string) string {
    parts := strings.SplitN(name, "__", 3)
    if len(parts) == 3 {
        return parts[2]
    }
    return name
}

// extractMCPText concatenates all text content blocks from an MCP tool result,
// separated by newlines. Non-text blocks (image, resource) are represented
// as "[<type> content]" placeholders.
func extractMCPText(result *mcp.ToolResult) string {
    var parts []string
    for _, block := range result.Content {
        switch block.Type {
        case "text":
            parts = append(parts, block.Text)
        default:
            parts = append(parts, fmt.Sprintf("[%s content]", block.Type))
        }
    }
    return strings.Join(parts, "\n")
}
```

### 3.4 — Tool name mapping

Claude sees namespaced names (`mcp__stump__stump`). The executor strips
the prefix when calling the MCP server (server expects just `stump`).

Collision handling:
- If a builtin and MCP tool share a name, builtin wins (user can disable
  the builtin via `DisabledTools` config)
- MCP tools are always namespaced in the registry to avoid silent
  collisions
- **Constraint:** Server names must not contain `__` (double underscore).
  Validate at config load time and reject with a clear error.

### Testing

- MCP tools appear in `registry.APITools()` output
- `executor.Execute("mcp__stump__stump", input)` → routes to MCP server
- Server disconnect → tools removed from registry
- Server reconnect → tools re-added
- Name collision: builtin `stump` + MCP `mcp__stump__stump` coexist

### 3.5 — System prompt awareness

The `agenticSystemPrompt` in `nostop.go` hardcodes the 18 builtin tool
names. MCP tools appear in the API tool definitions but not in this
prompt text. This is acceptable for Phases 1-4 because tool
descriptions in the API `tools` array provide sufficient context for
the model. If MCP tool adoption is low in testing, consider appending
a dynamic "Additional tools from MCP servers:" section to the system
prompt in a future iteration.

### 3.6 — Shutdown integration

Wire `ServerManager.StopAll()` into `Nostop.Close()`. Call it AFTER
`r.wg.Wait()` (to let in-flight tool calls finish) but BEFORE
`r.storage.Close()`. Use a context with 5-second timeout to prevent
hung servers from blocking shutdown indefinitely.

**Files:** `internal/tools/registry.go` (add `sync.RWMutex`, add
`MCPServer` field to `ToolDef`, add `IsMCPTool()`, update
`CheckBinaries()` to skip MCP tools),
`internal/tools/executor.go` (add `executeMCP` dispatch path, add
`mcpManager` field to `Executor`),
`pkg/nostop/nostop.go` (add `MCPManager()` accessor, wire
`StopAll()` into `Close()`)

---

## Phase 4: TUI Integration

**Goal:** Surface MCP server status and tools in the TUI.

### 4.1 — Startup display

During app initialization, show MCP server connection status:
```
Connecting MCP servers...
  stump: ready (1 tool)
  sig: ready (1 tool)
  remote: error (connection refused)
```

### 4.2 — Debug overlay extension

The `ServerManager` must be accessible from the TUI. Add a
`MCPManager() *mcp.ServerManager` method to the `Nostop` engine,
following the existing pattern of `ToolRegistry()` and
`ToolsEnabled()` accessors. Pass it to `NewDebugModel` (or let
`DebugModel` access it through the engine reference).

Add MCP section to the debug overlay:

```
MCP Servers
───────────
stump          Ready    1 tool     stdio
sig            Ready    1 tool     stdio
remote         Error    0 tools    http
```

### 4.3 — Tool call display

MCP tool calls already flow through the existing `ToolCallMsg` /
`ToolResultMsg` path since they're dispatched via the same executor.
The `ToolTarget` extraction and `formatToolOutput` in chat.go will work
as-is for MCP tools that return JSON.

### 4.4 — MCP status in header (DEFERRED)

Show MCP server count in the header when servers are configured.
Deferred until Phase 4.1-4.3 are validated — the header already
shows topic and activity status, adding more may crowd it.

### Testing

- Debug overlay shows MCP server status
- Tool calls from MCP servers render in chat like builtins
- Server error state visible in debug overlay

**Files:** `internal/tui/debug.go`, `internal/tui/app.go`,
`cmd/nostop/setup.go`

---

## Phase 5: Resources and Prompts (Future)

**Goal:** Support MCP resources (read-only data) and prompts (templates).

### 5.1 — Resources

MCP resources provide read-only access to data sources. These can be
used to:
- Inject documentation into context
- Read database state
- Fetch API responses

Resources are NOT tools — they don't appear in the tool list. They're
fetched by the client and injected into the system prompt or user
messages.

### 5.2 — Prompts

MCP prompts are reusable templates. They could be surfaced as:
- Slash commands in the TUI input
- Pre-built system prompts selectable per conversation

### 5.3 — Resource subscription

MCP supports resource change notifications. When a resource changes,
the server notifies the client, which can refresh its cached copy.

**Deferred:** Resources and prompts are lower priority than tools.
Phase 5 is included for completeness but should not block the tool
integration in Phases 1-4.

---

## Phase 6: Migration Path for Existing Tools

**Goal:** Optionally run existing subprocess tools as MCP servers instead
of via `exec.Command`, gaining connection reuse and richer output.

### Current subprocess tools that have MCP modes:

All 15 tools in `internal/tools/builtin_*.go` are **native Go
implementations** — they do not shell out to CLI binaries. The
subprocess dispatch path in `executor.go` exists but has zero active
tools (`definitions.go:AllTools()` returns an empty slice).

The external terse-mcp binaries (`stump`, `sig`, `repfor`, etc.) exist
independently at `/usr/local/bin/` and can run as MCP servers. If a
user configures one in `.mcp.json`, it would provide the same
functionality as the compiled-in builtin but via MCP transport.

### When this matters:

- A user wants a tool that exists as an MCP server but is NOT compiled
  into nostop as a builtin
- A user wants to use a newer version of a tool binary without
  rebuilding nostop
- Third-party MCP servers that have no builtin equivalent

### Collision resolution:

If both a builtin and an MCP server provide the same tool (e.g.,
builtin `stump` and MCP `mcp__stump__stump`), both are registered
under different names. The builtin is called by its short name, the
MCP version by its namespaced name. No migration needed — they coexist.

**Deferred:** This phase is informational. No code changes required.

---

## Execution Order

```
Phase 1 (protocol layer)     ← standalone, no existing code changes
    ↓
Phase 2 (server management)  ← depends on Phase 1 transport
    ↓
Phase 3 (registry integration) ← depends on Phase 2 for ServerManager
    ↓
Phase 4 (TUI integration)    ← depends on Phase 3 for tool visibility
    ↓
Phase 5 (resources/prompts)  ← deferred, independent of Phase 4
Phase 6 (migration path)     ← deferred, optimization only
```

Phases 1 and 2 can be developed without touching any existing files.
Phase 3 modifies `registry.go` and `executor.go`. Phase 4 modifies
TUI files.

---

## Risk Assessment

| Risk | Impact | Mitigation |
|------|--------|------------|
| stdio server crash | Tool calls fail | ServerManager detects crash via process watcher goroutine, marks server Error. On next tool call to that server, attempts one restart (re-spawn + Initialize). If restart fails, returns error to the tool loop. No automatic retry loop — failed restart stays in Error state until the next call. |
| Slow MCP server blocks tool loop | Streaming stalls | Per-call timeout (default 30s), configurable per server |
| Runaway tool loop with MCP server | Unbounded API billing | No iteration cap — the user is responsible for cancelling via ctrl+c. The tool loop is user-visible in the TUI (each tool call displayed). This matches the existing behavior for builtin/subprocess tools. |
| Tool name collisions | Wrong tool called | Namespace all MCP tools as `servername__toolname` |
| Large tool list overwhelms API | Token cost for tool definitions | Allow `disabled` per-server and per-tool filtering |
| `.mcp.json` compatibility | Config drift from Claude Code format | Track spec, test with real Claude Code configs |
| HTTP transport auth | Credentials in config | Support `${ENV_VAR}` expansion, never log headers |

---

## Dependents to Update

- `internal/tools` package — imported only by `pkg/nostop/nostop.go`.
  Changes to Registry, Executor, or ToolDef affect only this consumer.
- `pkg/nostop` package — imported by `cmd/nostop/main.go`,
  `cmd/nostop/setup.go`, `internal/tui/app.go`, `internal/tui/chat.go`,
  `internal/tui/debug.go`, `internal/tui/topics.go`. Any new public
  engine methods for MCP are accessible through these paths.
- The subprocess dispatch path (`executeSubprocess`) exists but has
  zero active tools. All 15 tools are native Go builtins.
  `definitions.go:AllTools()` returns an empty slice.

---

## Dependencies

- No external Go MCP library — implement protocol directly (it's
  JSON-RPC 2.0, not complex enough to warrant a dependency)
- Existing tools continue to work unchanged
- `.mcp.json` format is a de facto standard but not formally versioned —
  pin to the current Claude Code format and note any extensions
