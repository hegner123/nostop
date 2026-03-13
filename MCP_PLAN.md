# MCP Client Support Plan

> Add Model Context Protocol (MCP) client support to nostop, enabling
> connection to external MCP servers that provide tools, resources, and
> prompts. This lets nostop leverage the same ecosystem of MCP servers
> that Claude Code, Cursor, and other MCP-aware clients use.

## Status: Draft v1 (2026-03-13)

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
type Request struct {
    JSONRPC string         `json:"jsonrpc"`
    ID      int64          `json:"id"`
    Method  string         `json:"method"`
    Params  map[string]any `json:"params,omitempty"`
}

type Response struct {
    JSONRPC string         `json:"jsonrpc"`
    ID      int64          `json:"id"`
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
- `StdioTransport` — manages a subprocess, writes JSON-RPC to stdin,
  reads from stdout, line-delimited
- `HTTPTransport` — sends JSON-RPC over HTTP POST, handles SSE for
  server-initiated messages

### 1.3 — MCP client

Wraps a transport and implements the MCP protocol methods:

```go
type Client struct {
    transport Transport
    info      ServerInfo
    nextID    atomic.Int64
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
type ServerInfo struct {
    Name         string   `json:"name"`
    Version      string   `json:"version"`
    Capabilities []string `json:"capabilities"`
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

type ContentBlock struct {
    Type string `json:"type"` // "text", "image", "resource"
    Text string `json:"text,omitempty"`
    // image and resource fields as needed
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

Search order (first found wins):
1. `--mcp-config` CLI flag
2. `.mcp.json` in working directory
3. `.mcp.json` in project root (git root)
4. `~/.config/nostop/mcp.json`

Environment variable expansion in config values: `${VAR}` and
`${VAR:-default}`.

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

### 3.1 — New dispatch type in ToolDef

```go
type ToolDef struct {
    Name        string
    Description string
    InputSchema map[string]any

    // Dispatch — exactly one of these is set
    Builtin   BuiltinFunc          // in-process Go function
    Binary    string               // subprocess CLI tool
    MCPServer string               // MCP server name (dispatches via ServerManager)
}

func (d ToolDef) IsMCPTool() bool {
    return d.MCPServer != ""
}
```

### 3.2 — Tool discovery and registration

On startup (or when a server connects):
1. `ServerManager.Start()` calls `client.ListTools()`
2. Each `ToolInfo` becomes a `ToolDef` with `MCPServer` set
3. `Registry.Register()` adds it (namespaced: `servername__toolname` to
   avoid collisions, matching Claude Code convention)
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

func (e *Executor) executeMCP(ctx context.Context, def ToolDef, input map[string]any) Result {
    result, err := e.mcpManager.CallTool(ctx, def.MCPServer, def.Name, input)
    if err != nil {
        return Result{IsError: true, Error: err.Error()}
    }
    // Extract text content from MCP result
    return Result{Output: extractText(result), IsError: result.IsError}
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

### Testing

- MCP tools appear in `registry.APITools()` output
- `executor.Execute("mcp__stump__stump", input)` → routes to MCP server
- Server disconnect → tools removed from registry
- Server reconnect → tools re-added
- Name collision: builtin `stump` + MCP `mcp__stump__stump` coexist

**Files:** `internal/tools/registry.go`, `internal/tools/executor.go`

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

### 4.4 — MCP status in header

Optional: show MCP server count in the header when servers are configured:
```
nostop · 3 servers                              topic: Current Work
```

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

Many of the tools in `internal/tools/builtin_*.go` are Go functions that
call CLI binaries with `--cli` flags. These same binaries also serve as
MCP servers (invoked without `--cli`). For example:

- `stump` → `stump --cli` (subprocess) or `stump` (MCP stdio)
- `sig` → `sig --cli` (subprocess) or `sig` (MCP stdio)
- `repfor` → `repfor --cli` (subprocess) or `repfor` (MCP stdio)

### Benefits of MCP mode over subprocess:

| Aspect | Subprocess | MCP |
|--------|-----------|-----|
| Process lifecycle | New process per call | Long-lived, single process |
| Startup cost | Binary load + init each time | Once on connect |
| Output format | Stdout string | Structured JSON-RPC |
| Error reporting | Exit code + stderr | Typed error codes |
| Streaming | Not supported | Possible via SSE |

### Migration approach:

Not a breaking change — both paths coexist. Users can configure tools
as MCP servers in `.mcp.json` to get the benefits, or keep using the
builtin subprocess dispatch. The builtin dispatch acts as a fallback
when no MCP server is configured for a tool.

**Deferred:** This is an optimization, not a functional requirement.

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
| stdio server crash | Tool calls fail | ServerManager detects crash, marks server Error, retries on next call |
| Slow MCP server blocks tool loop | Streaming stalls | Per-call timeout (default 30s), configurable per server |
| Tool name collisions | Wrong tool called | Namespace all MCP tools as `servername__toolname` |
| Large tool list overwhelms API | Token cost for tool definitions | Allow `disabled` per-server and per-tool filtering |
| `.mcp.json` compatibility | Config drift from Claude Code format | Track spec, test with real Claude Code configs |
| HTTP transport auth | Credentials in config | Support `${ENV_VAR}` expansion, never log headers |

---

## Dependencies

- No external Go MCP library — implement protocol directly (it's
  JSON-RPC 2.0, not complex enough to warrant a dependency)
- Existing tools continue to work unchanged
- `.mcp.json` format is a de facto standard but not formally versioned —
  pin to the current Claude Code format and note any extensions
