# Model Context Protocol (MCP) — Complete Specification

**Version:** 2025-03-26
**Source:** https://modelcontextprotocol.io/specification/2025-03-26
**Schema:** https://github.com/modelcontextprotocol/specification/blob/main/schema/2025-03-26/schema.ts

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in BCP 14 [RFC2119] [RFC8174].

---

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture](#2-architecture)
3. [Base Protocol](#3-base-protocol)
4. [Lifecycle](#4-lifecycle)
5. [Transports](#5-transports)
6. [Authorization](#6-authorization)
7. [Server Features](#7-server-features)
   - [Prompts](#71-prompts)
   - [Resources](#72-resources)
   - [Tools](#73-tools)
8. [Client Features](#8-client-features)
   - [Roots](#81-roots)
   - [Sampling](#82-sampling)
9. [Utilities](#9-utilities)
   - [Cancellation](#91-cancellation)
   - [Progress](#92-progress)
   - [Ping](#93-ping)
   - [Logging](#94-logging)
   - [Pagination](#95-pagination)
   - [Completion](#96-completion)
10. [TypeScript Schema](#10-typescript-schema)

---

## 1. Overview

Model Context Protocol (MCP) is an open protocol that enables seamless integration between LLM applications and external data sources and tools. It provides a standardized way to connect LLMs with the context they need.

MCP provides a standardized way for applications to:

- Share contextual information with language models
- Expose tools and capabilities to AI systems
- Build composable integrations and workflows

The protocol uses **JSON-RPC 2.0** messages to establish communication between:

- **Hosts**: LLM applications that initiate connections
- **Clients**: Connectors within the host application
- **Servers**: Services that provide context and capabilities

### Key Details

**Base Protocol:**
- JSON-RPC message format
- Stateful connections
- Server and client capability negotiation

**Server Features:**
- **Resources**: Context and data, for the user or the AI model to use
- **Prompts**: Templated messages and workflows for users
- **Tools**: Functions for the AI model to execute

**Client Features:**
- **Sampling**: Server-initiated agentic behaviors and recursive LLM interactions

**Utilities:**
- Configuration, Progress tracking, Cancellation, Error reporting, Logging

### Security and Trust & Safety

#### Key Principles

1. **User Consent and Control** — Users must explicitly consent to and understand all data access and operations. Implementors should provide clear UIs for reviewing and authorizing activities.

2. **Data Privacy** — Hosts must obtain explicit user consent before exposing user data to servers. Hosts must not transmit resource data elsewhere without user consent.

3. **Tool Safety** — Tools represent arbitrary code execution and must be treated with appropriate caution. Descriptions of tool behavior such as annotations should be considered untrusted unless obtained from a trusted server. Hosts must obtain explicit user consent before invoking any tool.

4. **LLM Sampling Controls** — Users must explicitly approve any LLM sampling requests. Users should control whether sampling occurs, the actual prompt sent, and what results the server can see.

---

## 2. Architecture

MCP follows a **client-host-server** architecture where each host can run multiple client instances.

```
┌─────────────────────────────────────────────┐
│              Application Host               │
│                                             │
│  ┌────────┐  ┌────────┐  ┌────────┐        │
│  │Client 1│  │Client 2│  │Client 3│        │
│  └───┬────┘  └───┬────┘  └───┬────┘        │
│      │           │           │              │
└──────┼───────────┼───────────┼──────────────┘
       │           │           │
  ┌────┴────┐ ┌────┴────┐ ┌───┴─────┐
  │Server 1 │ │Server 2 │ │Server 3 │
  │Files&Git│ │Database │ │Ext. APIs│
  └─────────┘ └─────────┘ └─────────┘
```

### Host

The host process acts as the container and coordinator:

- Creates and manages multiple client instances
- Controls client connection permissions and lifecycle
- Enforces security policies and consent requirements
- Handles user authorization decisions
- Coordinates AI/LLM integration and sampling
- Manages context aggregation across clients

### Clients

Each client is created by the host and maintains an isolated server connection:

- Establishes one stateful session per server
- Handles protocol negotiation and capability exchange
- Routes protocol messages bidirectionally
- Manages subscriptions and notifications
- Maintains security boundaries between servers

A host application creates and manages multiple clients, with each client having a **1:1 relationship** with a particular server.

### Servers

Servers provide specialized context and capabilities:

- Expose resources, tools and prompts via MCP primitives
- Operate independently with focused responsibilities
- Request sampling through client interfaces
- Must respect security constraints
- Can be local processes or remote services

### Design Principles

1. **Servers should be extremely easy to build** — Host applications handle complex orchestration responsibilities.
2. **Servers should be highly composable** — Each server provides focused functionality in isolation.
3. **Servers should not be able to read the whole conversation, nor "see into" other servers** — Servers receive only necessary contextual information.
4. **Features can be added progressively** — Core protocol provides minimal required functionality; additional capabilities negotiated as needed.

---

## 3. Base Protocol

### Messages

All messages between MCP clients and servers MUST follow the JSON-RPC 2.0 specification. The protocol defines these types of messages:

#### Requests

Requests are sent from the client to the server or vice versa, to initiate an operation.

```json
{
  "jsonrpc": "2.0",
  "id": "<string | number>",
  "method": "<string>",
  "params": { }
}
```

- Requests MUST include a string or integer ID.
- Unlike base JSON-RPC, the ID MUST NOT be `null`.
- The request ID MUST NOT have been previously used by the requestor within the same session.

#### Responses

Responses are sent in reply to requests, containing the result or error.

```json
{
  "jsonrpc": "2.0",
  "id": "<string | number>",
  "result": { },
  "error": {
    "code": "<number>",
    "message": "<string>",
    "data": "<unknown>"
  }
}
```

- Responses MUST include the same ID as the request they correspond to.
- Either a `result` or an `error` MUST be set. A response MUST NOT set both.
- Error codes MUST be integers.

**Standard JSON-RPC Error Codes:**

| Code | Constant | Description |
|------|----------|-------------|
| -32700 | PARSE_ERROR | Invalid JSON |
| -32600 | INVALID_REQUEST | Invalid JSON-RPC request |
| -32601 | METHOD_NOT_FOUND | Method not found |
| -32602 | INVALID_PARAMS | Invalid method parameters |
| -32603 | INTERNAL_ERROR | Internal error |

#### Notifications

One-way messages. The receiver MUST NOT send a response.

```json
{
  "jsonrpc": "2.0",
  "method": "<string>",
  "params": { }
}
```

- Notifications MUST NOT include an ID.

#### Batching

MCP implementations MAY support sending JSON-RPC batches, but MUST support receiving JSON-RPC batches. Batches are arrays containing one or more requests and/or notifications.

---

## 4. Lifecycle

The MCP lifecycle has three phases:

1. **Initialization**: Capability negotiation and protocol version agreement
2. **Operation**: Normal protocol communication
3. **Shutdown**: Graceful termination of the connection

### Initialization

The initialization phase MUST be the first interaction between client and server. During this phase, the client and server:

- Establish protocol version compatibility
- Exchange and negotiate capabilities
- Share implementation details

**Client sends `initialize` request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-03-26",
    "capabilities": {
      "roots": {
        "listChanged": true
      },
      "sampling": {}
    },
    "clientInfo": {
      "name": "ExampleClient",
      "version": "1.0.0"
    }
  }
}
```

The initialize request MUST NOT be part of a JSON-RPC batch.

**Server responds with capabilities:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-03-26",
    "capabilities": {
      "logging": {},
      "prompts": { "listChanged": true },
      "resources": { "subscribe": true, "listChanged": true },
      "tools": { "listChanged": true }
    },
    "serverInfo": {
      "name": "ExampleServer",
      "version": "1.0.0"
    },
    "instructions": "Optional instructions for the client"
  }
}
```

**Client sends `initialized` notification:**

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/initialized"
}
```

- The client SHOULD NOT send requests other than pings before the server has responded to the `initialize` request.
- The server SHOULD NOT send requests other than pings and logging before receiving the `initialized` notification.

### Version Negotiation

- Client MUST send a protocol version it supports (SHOULD be the latest).
- If the server supports the requested version, it MUST respond with the same version.
- Otherwise, the server MUST respond with another version it supports (SHOULD be the latest it supports).
- If the client does not support the server's version, it SHOULD disconnect.

### Capability Negotiation

| Category | Capability | Description |
|----------|-----------|-------------|
| Client | `roots` | Ability to provide filesystem roots |
| Client | `sampling` | Support for LLM sampling requests |
| Client | `experimental` | Non-standard experimental features |
| Server | `prompts` | Offers prompt templates |
| Server | `resources` | Provides readable resources |
| Server | `tools` | Exposes callable tools |
| Server | `logging` | Emits structured log messages |
| Server | `completions` | Supports argument autocompletion |
| Server | `experimental` | Non-standard experimental features |

Sub-capabilities:
- `listChanged`: Support for list change notifications (prompts, resources, tools)
- `subscribe`: Support for subscribing to individual items' changes (resources only)

### Operation

During operation, both parties SHOULD:
- Respect the negotiated protocol version
- Only use capabilities that were successfully negotiated

### Shutdown

No specific shutdown messages are defined—the underlying transport mechanism signals connection termination.

**stdio:** Client SHOULD close the input stream, wait for the server to exit, send SIGTERM if needed, then SIGKILL if still not exited.

**HTTP:** Shutdown indicated by closing associated HTTP connection(s).

### Timeouts

Implementations SHOULD establish timeouts for all sent requests. When a request times out, the sender SHOULD issue a cancellation notification and stop waiting. SDKs SHOULD allow per-request timeout configuration. Implementations MAY reset the timeout clock when receiving a progress notification.

---

## 5. Transports

MCP uses JSON-RPC to encode messages. Messages MUST be UTF-8 encoded. Two standard transport mechanisms:

### stdio

- Client launches the MCP server as a subprocess.
- Server reads JSON-RPC messages from `stdin` and sends messages to `stdout`.
- Messages may be requests, notifications, responses, or JSON-RPC batches.
- Messages are delimited by newlines, and MUST NOT contain embedded newlines.
- Server MAY write UTF-8 strings to `stderr` for logging.
- Server MUST NOT write non-MCP content to `stdout`.
- Client MUST NOT write non-MCP content to server's `stdin`.

### Streamable HTTP

Replaces the HTTP+SSE transport from protocol version 2024-11-05.

The server MUST provide a single HTTP endpoint (the **MCP endpoint**) that supports both POST and GET methods.

#### Security Warning

- Servers MUST validate the `Origin` header on all incoming connections (DNS rebinding prevention).
- When running locally, servers SHOULD bind only to localhost (127.0.0.1).
- Servers SHOULD implement proper authentication.

#### Sending Messages to the Server (POST)

1. Client MUST use HTTP POST to send JSON-RPC messages.
2. Client MUST include `Accept` header listing both `application/json` and `text/event-stream`.
3. POST body MUST be one of:
   - A single JSON-RPC request, notification, or response
   - An array batching one or more requests and/or notifications
   - An array batching one or more responses
4. If input consists solely of responses or notifications:
   - Server MUST return HTTP 202 Accepted with no body.
   - If rejected, server MUST return an HTTP error (e.g., 400).
5. If input contains any requests, server MUST return either:
   - `Content-Type: text/event-stream` (SSE stream), or
   - `Content-Type: application/json` (single JSON object)
6. If SSE stream:
   - SHOULD eventually include one response per request.
   - Server MAY send requests/notifications before sending responses.
   - Server SHOULD NOT close the stream before sending all responses.
   - Disconnection SHOULD NOT be interpreted as cancellation.

#### Listening for Messages from the Server (GET)

1. Client MAY issue HTTP GET to the MCP endpoint to open an SSE stream.
2. Client MUST include `Accept: text/event-stream`.
3. Server MUST either return `Content-Type: text/event-stream` or HTTP 405.
4. If SSE stream, server MAY send requests and notifications (but MUST NOT send responses unless resuming).

#### Multiple Connections

- Client MAY connect to multiple SSE streams simultaneously.
- Server MUST send each message on only one stream (no broadcasting).

#### Resumability and Redelivery

- Servers MAY attach `id` fields to SSE events. IDs MUST be globally unique within a session.
- Clients wishing to resume after disconnect SHOULD issue HTTP GET with `Last-Event-ID` header.
- Server MAY replay messages from after the last event ID on the stream that was disconnected.
- Server MUST NOT replay messages from a different stream.

#### Session Management

1. Server MAY assign a session ID via `Mcp-Session-Id` header on the response containing `InitializeResult`.
   - Session ID SHOULD be globally unique and cryptographically secure.
   - MUST only contain visible ASCII characters (0x21 to 0x7E).
2. Clients MUST include `Mcp-Session-Id` in all subsequent HTTP requests.
   - Servers that require a session ID SHOULD respond to requests without one with HTTP 400.
3. Server MAY terminate sessions at any time (responds with HTTP 404).
4. When client receives HTTP 404 with a session ID, it MUST start a new session.
5. Clients SHOULD send HTTP DELETE with `Mcp-Session-Id` to explicitly terminate sessions.

### Custom Transports

Clients and servers MAY implement additional custom transports. They MUST preserve the JSON-RPC message format and lifecycle requirements.

---

## 6. Authorization

Authorization is OPTIONAL for MCP implementations.

- HTTP-based transports SHOULD conform to this specification.
- STDIO transports SHOULD NOT follow this specification (retrieve credentials from environment).

### Standards

Based on:
- OAuth 2.1 IETF DRAFT
- OAuth 2.0 Authorization Server Metadata (RFC 8414)
- OAuth 2.0 Dynamic Client Registration Protocol (RFC 7591)

### Requirements

1. MCP auth implementations MUST implement OAuth 2.1.
2. MCP auth implementations SHOULD support Dynamic Client Registration (RFC 7591).
3. MCP servers SHOULD and MCP clients MUST implement Authorization Server Metadata (RFC 8414). Servers that do not MUST follow the default URI schema.

### OAuth Grant Types

- **Authorization Code**: client acting on behalf of a human end user.
- **Client Credentials**: client is another application (not a human).

### Authorization Code Flow (Example)

1. Client sends MCP request → Server returns HTTP 401 Unauthorized.
2. Client generates `code_verifier` and `code_challenge`.
3. Client opens browser with authorization URL + `code_challenge`.
4. User logs in and authorizes.
5. Redirect to callback URL with auth code.
6. Client exchanges code + `code_verifier` for access token (+ refresh token).
7. Client sends MCP request with access token.

### Server Metadata Discovery

- Clients MUST follow RFC 8414 for discovery.
- Authorization base URL: the MCP server URL with the `path` component discarded.
  - Example: MCP server at `https://api.example.com/v1/mcp` → metadata at `https://api.example.com/.well-known/oauth-authorization-server`
- Clients SHOULD include `MCP-Protocol-Version` header during discovery.

**Fallback Endpoints (when metadata discovery not supported):**

| Endpoint | Default Path |
|----------|-------------|
| Authorization | /authorize |
| Token | /token |
| Registration | /register |

### Dynamic Client Registration

MCP clients and servers SHOULD support RFC 7591. This is crucial because:
- Clients cannot know all possible servers in advance
- Manual registration creates friction
- Enables seamless connection to new servers

### Access Token Usage

- Client MUST use `Authorization: Bearer <access-token>` header.
- Authorization MUST be included in every HTTP request (even within the same session).
- Access tokens MUST NOT be in URI query strings.

### Third-Party Authorization Flow

MCP servers MAY support delegated authorization through third-party authorization servers:

1. MCP client initiates standard OAuth flow with MCP server
2. MCP server redirects to third-party authorization server
3. User authorizes with third-party server
4. Third-party server redirects back to MCP server
5. MCP server exchanges code for third-party access token
6. MCP server generates its own bound access token
7. MCP server completes original OAuth flow with MCP client

### Security Requirements

- Clients MUST securely store tokens
- Servers SHOULD enforce token expiration and rotation
- All authorization endpoints MUST be served over HTTPS
- Servers MUST validate redirect URIs
- Redirect URIs MUST be either localhost URLs or HTTPS URLs
- PKCE is REQUIRED for all clients

---

## 7. Server Features

Servers provide three primitives:

| Primitive | Control | Description | Example |
|-----------|---------|-------------|---------|
| Prompts | User-controlled | Interactive templates invoked by user choice | Slash commands, menu options |
| Resources | Application-controlled | Contextual data attached and managed by the client | File contents, git history |
| Tools | Model-controlled | Functions exposed to the LLM to take actions | API POST requests, file writing |

### 7.1 Prompts

Prompts allow servers to provide structured messages and instructions for interacting with language models. Designed to be **user-controlled**.

#### Capabilities

```json
{
  "capabilities": {
    "prompts": {
      "listChanged": true
    }
  }
}
```

#### Listing Prompts — `prompts/list`

Supports pagination.

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "prompts/list",
  "params": {
    "cursor": "optional-cursor-value"
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "prompts": [
      {
        "name": "code_review",
        "description": "Asks the LLM to analyze code quality and suggest improvements",
        "arguments": [
          {
            "name": "code",
            "description": "The code to review",
            "required": true
          }
        ]
      }
    ],
    "nextCursor": "next-page-cursor"
  }
}
```

#### Getting a Prompt — `prompts/get`

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "prompts/get",
  "params": {
    "name": "code_review",
    "arguments": {
      "code": "def hello():\n    print('world')"
    }
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "description": "Code review prompt",
    "messages": [
      {
        "role": "user",
        "content": {
          "type": "text",
          "text": "Please review this Python code:\ndef hello():\n    print('world')"
        }
      }
    ]
  }
}
```

#### List Changed Notification

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/prompts/list_changed"
}
```

#### Data Types

**Prompt:** `name` (string), `description` (optional string), `arguments` (optional array of PromptArgument).

**PromptArgument:** `name` (string), `description` (optional string), `required` (optional boolean).

**PromptMessage:** `role` ("user" | "assistant"), `content` (TextContent | ImageContent | AudioContent | EmbeddedResource).

**Content Types:**
- TextContent: `{ "type": "text", "text": "..." }`
- ImageContent: `{ "type": "image", "data": "<base64>", "mimeType": "image/png" }`
- AudioContent: `{ "type": "audio", "data": "<base64>", "mimeType": "audio/wav" }`
- EmbeddedResource: `{ "type": "resource", "resource": { "uri": "...", "mimeType": "...", "text": "..." } }`

### 7.2 Resources

Resources allow servers to share data that provides context to language models (files, database schemas, application-specific information). Each resource is uniquely identified by a URI. Designed to be **application-driven**.

#### Capabilities

```json
{
  "capabilities": {
    "resources": {
      "subscribe": true,
      "listChanged": true
    }
  }
}
```

Both `subscribe` and `listChanged` are optional.

#### Listing Resources — `resources/list`

Supports pagination.

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "resources/list",
  "params": { "cursor": "optional-cursor-value" }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "resources": [
      {
        "uri": "file:///project/src/main.rs",
        "name": "main.rs",
        "description": "Primary application entry point",
        "mimeType": "text/x-rust"
      }
    ],
    "nextCursor": "next-page-cursor"
  }
}
```

#### Reading Resources — `resources/read`

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "resources/read",
  "params": { "uri": "file:///project/src/main.rs" }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "contents": [
      {
        "uri": "file:///project/src/main.rs",
        "mimeType": "text/x-rust",
        "text": "fn main() {\n    println!(\"Hello world!\");\n}"
      }
    ]
  }
}
```

#### Resource Templates — `resources/templates/list`

Supports pagination. Uses URI templates (RFC 6570).

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "resourceTemplates": [
      {
        "uriTemplate": "file:///{path}",
        "name": "Project Files",
        "description": "Access files in the project directory",
        "mimeType": "application/octet-stream"
      }
    ]
  }
}
```

#### List Changed Notification

```json
{ "jsonrpc": "2.0", "method": "notifications/resources/list_changed" }
```

#### Subscriptions

**Subscribe:**
```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "resources/subscribe",
  "params": { "uri": "file:///project/src/main.rs" }
}
```

**Unsubscribe:**
```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "resources/unsubscribe",
  "params": { "uri": "file:///project/src/main.rs" }
}
```

**Update Notification:**
```json
{
  "jsonrpc": "2.0",
  "method": "notifications/resources/updated",
  "params": { "uri": "file:///project/src/main.rs" }
}
```

#### Data Types

**Resource:** `uri` (string, format: uri), `name` (string), `description` (optional), `mimeType` (optional), `size` (optional, bytes), `annotations` (optional).

**Resource Contents:**
- Text: `{ "uri": "...", "mimeType": "...", "text": "..." }`
- Binary: `{ "uri": "...", "mimeType": "...", "blob": "<base64>" }`

#### Common URI Schemes

- `https://` — Resource available on the web (server SHOULD use only when client can fetch directly).
- `file://` — Filesystem-like resources (need not map to actual filesystem).
- `git://` — Git version control integration.

#### Error Codes

- Resource not found: `-32002`
- Internal errors: `-32603`

### 7.3 Tools

Tools enable models to interact with external systems. Each tool is uniquely identified by a name and includes metadata describing its schema. Designed to be **model-controlled**.

#### User Interaction Model

There SHOULD always be a human in the loop with the ability to deny tool invocations.

Applications SHOULD:
- Make clear which tools are exposed to the AI model
- Insert clear visual indicators when tools are invoked
- Present confirmation prompts for operations

#### Capabilities

```json
{
  "capabilities": {
    "tools": {
      "listChanged": true
    }
  }
}
```

#### Listing Tools — `tools/list`

Supports pagination.

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": { "cursor": "optional-cursor-value" }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "tools": [
      {
        "name": "get_weather",
        "description": "Get current weather information for a location",
        "inputSchema": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "City name or zip code"
            }
          },
          "required": ["location"]
        }
      }
    ],
    "nextCursor": "next-page-cursor"
  }
}
```

#### Calling Tools — `tools/call`

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "get_weather",
    "arguments": { "location": "New York" }
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Current weather in New York:\nTemperature: 72°F\nConditions: Partly cloudy"
      }
    ],
    "isError": false
  }
}
```

#### List Changed Notification

```json
{ "jsonrpc": "2.0", "method": "notifications/tools/list_changed" }
```

#### Data Types

**Tool:**
- `name` (string): Unique identifier
- `description` (optional string): Human-readable description
- `inputSchema` (object): JSON Schema defining expected parameters
- `annotations` (optional ToolAnnotations): Properties describing tool behavior

**ToolAnnotations** (all are hints, not guarantees):
- `title` (optional string): Human-readable title
- `readOnlyHint` (optional boolean, default false): Tool does not modify its environment
- `destructiveHint` (optional boolean, default true): Tool may perform destructive updates (meaningful only when readOnlyHint is false)
- `idempotentHint` (optional boolean, default false): Calling repeatedly with same args has no additional effect (meaningful only when readOnlyHint is false)
- `openWorldHint` (optional boolean, default true): Tool may interact with external entities

Clients MUST consider tool annotations untrusted unless from trusted servers.

**Tool Result:**
- `content` (array of TextContent | ImageContent | AudioContent | EmbeddedResource)
- `isError` (optional boolean): Whether the tool call ended in an error

#### Error Handling

Two error reporting mechanisms:

1. **Protocol Errors** — Standard JSON-RPC errors (unknown tools, invalid arguments, server errors)
2. **Tool Execution Errors** — Reported in tool results with `isError: true` (API failures, invalid input, business logic errors)

Tool errors SHOULD be reported inside the result (with `isError: true`) so the LLM can see and self-correct. Protocol-level errors (tool not found, server doesn't support tools) use standard JSON-RPC error responses.

#### Security

Servers MUST: validate all tool inputs, implement access controls, rate limit invocations, sanitize outputs.

Clients SHOULD: prompt for user confirmation, show tool inputs before calling, validate results, implement timeouts, log usage.

---

## 8. Client Features

### 8.1 Roots

Roots define the boundaries of where servers can operate within the filesystem.

#### Capabilities

```json
{
  "capabilities": {
    "roots": {
      "listChanged": true
    }
  }
}
```

#### Listing Roots — `roots/list`

**Request:**
```json
{ "jsonrpc": "2.0", "id": 1, "method": "roots/list" }
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "roots": [
      {
        "uri": "file:///home/user/projects/myproject",
        "name": "My Project"
      }
    ]
  }
}
```

#### Root List Changes

```json
{ "jsonrpc": "2.0", "method": "notifications/roots/list_changed" }
```

#### Data Types

**Root:**
- `uri` (string, format: uri): MUST be a `file://` URI
- `name` (optional string): Human-readable name

### 8.2 Sampling

Sampling allows servers to request LLM completions from clients. The client maintains control over model access, selection, and permissions.

There SHOULD always be a human in the loop with ability to deny sampling requests.

#### Capabilities

```json
{
  "capabilities": {
    "sampling": {}
  }
}
```

#### Creating Messages — `sampling/createMessage`

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "sampling/createMessage",
  "params": {
    "messages": [
      {
        "role": "user",
        "content": {
          "type": "text",
          "text": "What is the capital of France?"
        }
      }
    ],
    "modelPreferences": {
      "hints": [{ "name": "claude-3-sonnet" }],
      "intelligencePriority": 0.8,
      "speedPriority": 0.5
    },
    "systemPrompt": "You are a helpful assistant.",
    "includeContext": "none",
    "maxTokens": 100
  }
}
```

**Additional request params:**
- `temperature` (optional number)
- `stopSequences` (optional string array)
- `metadata` (optional object, provider-specific)
- `includeContext` ("none" | "thisServer" | "allServers")

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "role": "assistant",
    "content": {
      "type": "text",
      "text": "The capital of France is Paris."
    },
    "model": "claude-3-sonnet-20240307",
    "stopReason": "endTurn"
  }
}
```

#### Model Preferences

**Capability Priorities** (normalized 0-1):
- `costPriority`: Higher = prefer cheaper models
- `speedPriority`: Higher = prefer faster models
- `intelligencePriority`: Higher = prefer more capable models

**Model Hints:**
- Treated as substrings matching model names flexibly
- Multiple hints evaluated in order of preference
- Clients MAY map hints to different providers' equivalent models
- Hints are advisory; clients make final selection

Example:
```json
{
  "hints": [
    { "name": "claude-3-sonnet" },
    { "name": "claude" }
  ],
  "costPriority": 0.3,
  "speedPriority": 0.8,
  "intelligencePriority": 0.5
}
```

#### Message Flow

1. Server sends `sampling/createMessage` to client
2. Client presents request for user approval (human-in-the-loop)
3. User reviews and approves/modifies
4. Client forwards to LLM
5. LLM returns generation
6. Client presents response for user review
7. User approves/modifies
8. Client returns approved response to server

---

## 9. Utilities

### 9.1 Cancellation

Either side can send a cancellation notification for in-progress requests.

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/cancelled",
  "params": {
    "requestId": "123",
    "reason": "User requested cancellation"
  }
}
```

**Rules:**
- Cancellation notifications MUST only reference requests that were previously issued in the same direction and are believed to still be in-progress.
- The `initialize` request MUST NOT be cancelled by clients.
- Receivers SHOULD stop processing and free resources. MAY ignore if unknown or already completed.
- Sender SHOULD ignore any response arriving after cancellation.
- Both parties MUST handle race conditions gracefully.

### 9.2 Progress

Optional progress tracking for long-running operations.

**Requesting progress:** Include a `progressToken` in request metadata:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "some_method",
  "params": {
    "_meta": {
      "progressToken": "abc123"
    }
  }
}
```

**Progress notification:**
```json
{
  "jsonrpc": "2.0",
  "method": "notifications/progress",
  "params": {
    "progressToken": "abc123",
    "progress": 50,
    "total": 100,
    "message": "Reticulating splines..."
  }
}
```

- Progress tokens MUST be a string or integer, unique across all active requests.
- The `progress` value MUST increase with each notification.
- `progress` and `total` MAY be floating point.
- `total` may be omitted if unknown.
- `message` SHOULD provide relevant human-readable progress information.
- Receivers MAY choose not to send any progress notifications.

### 9.3 Ping

Either party can verify the other is responsive.

**Request:**
```json
{ "jsonrpc": "2.0", "id": "123", "method": "ping" }
```

**Response:**
```json
{ "jsonrpc": "2.0", "id": "123", "result": {} }
```

- Receiver MUST respond promptly with an empty response.
- If no response within timeout, sender MAY consider the connection stale, terminate, or attempt reconnection.
- Implementations SHOULD periodically issue pings with configurable frequency.

### 9.4 Logging

Servers send structured log messages to clients.

#### Capabilities

```json
{ "capabilities": { "logging": {} } }
```

#### Log Levels (RFC 5424 syslog severity)

| Level | Description |
|-------|-------------|
| debug | Detailed debugging information |
| info | General informational messages |
| notice | Normal but significant events |
| warning | Warning conditions |
| error | Error conditions |
| critical | Critical conditions |
| alert | Action must be taken immediately |
| emergency | System is unusable |

#### Setting Log Level — `logging/setLevel`

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "logging/setLevel",
  "params": { "level": "info" }
}
```

The server should send all logs at this level and higher (more severe).

#### Log Message Notification — `notifications/message`

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/message",
  "params": {
    "level": "error",
    "logger": "database",
    "data": {
      "error": "Connection failed",
      "details": { "host": "localhost", "port": 5432 }
    }
  }
}
```

- `level` (LoggingLevel): Severity
- `logger` (optional string): Logger name
- `data` (unknown): Any JSON-serializable data

**Security:** Log messages MUST NOT contain credentials, secrets, PII, or internal system details that could aid attacks.

### 9.5 Pagination

Cursor-based pagination for list operations.

- The cursor is an opaque string token.
- Page size is determined by the server; clients MUST NOT assume a fixed page size.

**Response with cursor:**
```json
{
  "result": {
    "resources": [...],
    "nextCursor": "eyJwYWdlIjogM30="
  }
}
```

**Request with cursor:**
```json
{
  "method": "resources/list",
  "params": { "cursor": "eyJwYWdlIjogMn0=" }
}
```

**Operations supporting pagination:**
- `resources/list`
- `resources/templates/list`
- `prompts/list`
- `tools/list`

**Rules:**
- Missing `nextCursor` = end of results.
- Clients MUST treat cursors as opaque tokens (don't parse, modify, or persist across sessions).
- Invalid cursors SHOULD result in error code `-32602`.

### 9.6 Completion

Servers offer argument autocompletion suggestions for prompts and resource URIs.

#### Capabilities

```json
{ "capabilities": { "completions": {} } }
```

#### Requesting Completions — `completion/complete`

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "completion/complete",
  "params": {
    "ref": {
      "type": "ref/prompt",
      "name": "code_review"
    },
    "argument": {
      "name": "language",
      "value": "py"
    }
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "completion": {
      "values": ["python", "pytorch", "pyside"],
      "total": 10,
      "hasMore": true
    }
  }
}
```

**Reference Types:**
- `ref/prompt`: `{ "type": "ref/prompt", "name": "..." }`
- `ref/resource`: `{ "type": "ref/resource", "uri": "..." }`

**Rules:**
- Maximum 100 items per response.
- `total` (optional): total available matches.
- `hasMore` (boolean): additional results exist.
- Servers SHOULD return suggestions sorted by relevance.
- Clients SHOULD debounce rapid requests and cache results.

---

## 10. TypeScript Schema

The authoritative type definitions for MCP protocol version 2025-03-26.

```typescript
/* JSON-RPC types */

export type JSONRPCMessage =
  | JSONRPCRequest
  | JSONRPCNotification
  | JSONRPCBatchRequest
  | JSONRPCResponse
  | JSONRPCError
  | JSONRPCBatchResponse;

export type JSONRPCBatchRequest = (JSONRPCRequest | JSONRPCNotification)[];
export type JSONRPCBatchResponse = (JSONRPCResponse | JSONRPCError)[];

export const LATEST_PROTOCOL_VERSION = "2025-03-26";
export const JSONRPC_VERSION = "2.0";

export type ProgressToken = string | number;
export type Cursor = string;

export interface Request {
  method: string;
  params?: {
    _meta?: {
      progressToken?: ProgressToken;
    };
    [key: string]: unknown;
  };
}

export interface Notification {
  method: string;
  params?: {
    _meta?: { [key: string]: unknown };
    [key: string]: unknown;
  };
}

export interface Result {
  _meta?: { [key: string]: unknown };
  [key: string]: unknown;
}

export type RequestId = string | number;

export interface JSONRPCRequest extends Request {
  jsonrpc: typeof JSONRPC_VERSION;
  id: RequestId;
}

export interface JSONRPCNotification extends Notification {
  jsonrpc: typeof JSONRPC_VERSION;
}

export interface JSONRPCResponse {
  jsonrpc: typeof JSONRPC_VERSION;
  id: RequestId;
  result: Result;
}

// Standard JSON-RPC error codes
export const PARSE_ERROR = -32700;
export const INVALID_REQUEST = -32600;
export const METHOD_NOT_FOUND = -32601;
export const INVALID_PARAMS = -32602;
export const INTERNAL_ERROR = -32603;

export interface JSONRPCError {
  jsonrpc: typeof JSONRPC_VERSION;
  id: RequestId;
  error: {
    code: number;
    message: string;
    data?: unknown;
  };
}

export type EmptyResult = Result;

/* Cancellation */

export interface CancelledNotification extends Notification {
  method: "notifications/cancelled";
  params: {
    requestId: RequestId;
    reason?: string;
  };
}

/* Initialization */

export interface InitializeRequest extends Request {
  method: "initialize";
  params: {
    protocolVersion: string;
    capabilities: ClientCapabilities;
    clientInfo: Implementation;
  };
}

export interface InitializeResult extends Result {
  protocolVersion: string;
  capabilities: ServerCapabilities;
  serverInfo: Implementation;
  instructions?: string;
}

export interface InitializedNotification extends Notification {
  method: "notifications/initialized";
}

export interface ClientCapabilities {
  experimental?: { [key: string]: object };
  roots?: {
    listChanged?: boolean;
  };
  sampling?: object;
}

export interface ServerCapabilities {
  experimental?: { [key: string]: object };
  logging?: object;
  completions?: object;
  prompts?: {
    listChanged?: boolean;
  };
  resources?: {
    subscribe?: boolean;
    listChanged?: boolean;
  };
  tools?: {
    listChanged?: boolean;
  };
}

export interface Implementation {
  name: string;
  version: string;
}

/* Ping */

export interface PingRequest extends Request {
  method: "ping";
}

/* Progress */

export interface ProgressNotification extends Notification {
  method: "notifications/progress";
  params: {
    progressToken: ProgressToken;
    progress: number;
    total?: number;
    message?: string;
  };
}

/* Pagination */

export interface PaginatedRequest extends Request {
  params?: {
    cursor?: Cursor;
  };
}

export interface PaginatedResult extends Result {
  nextCursor?: Cursor;
}

/* Resources */

export interface ListResourcesRequest extends PaginatedRequest {
  method: "resources/list";
}

export interface ListResourcesResult extends PaginatedResult {
  resources: Resource[];
}

export interface ListResourceTemplatesRequest extends PaginatedRequest {
  method: "resources/templates/list";
}

export interface ListResourceTemplatesResult extends PaginatedResult {
  resourceTemplates: ResourceTemplate[];
}

export interface ReadResourceRequest extends Request {
  method: "resources/read";
  params: {
    uri: string; // @format uri
  };
}

export interface ReadResourceResult extends Result {
  contents: (TextResourceContents | BlobResourceContents)[];
}

export interface ResourceListChangedNotification extends Notification {
  method: "notifications/resources/list_changed";
}

export interface SubscribeRequest extends Request {
  method: "resources/subscribe";
  params: { uri: string };
}

export interface UnsubscribeRequest extends Request {
  method: "resources/unsubscribe";
  params: { uri: string };
}

export interface ResourceUpdatedNotification extends Notification {
  method: "notifications/resources/updated";
  params: { uri: string };
}

export interface Resource {
  uri: string;       // @format uri
  name: string;
  description?: string;
  mimeType?: string;
  annotations?: Annotations;
  size?: number;
}

export interface ResourceTemplate {
  uriTemplate: string; // @format uri-template
  name: string;
  description?: string;
  mimeType?: string;
  annotations?: Annotations;
}

export interface ResourceContents {
  uri: string;
  mimeType?: string;
}

export interface TextResourceContents extends ResourceContents {
  text: string;
}

export interface BlobResourceContents extends ResourceContents {
  blob: string; // @format byte (base64)
}

/* Prompts */

export interface ListPromptsRequest extends PaginatedRequest {
  method: "prompts/list";
}

export interface ListPromptsResult extends PaginatedResult {
  prompts: Prompt[];
}

export interface GetPromptRequest extends Request {
  method: "prompts/get";
  params: {
    name: string;
    arguments?: { [key: string]: string };
  };
}

export interface GetPromptResult extends Result {
  description?: string;
  messages: PromptMessage[];
}

export interface Prompt {
  name: string;
  description?: string;
  arguments?: PromptArgument[];
}

export interface PromptArgument {
  name: string;
  description?: string;
  required?: boolean;
}

export type Role = "user" | "assistant";

export interface PromptMessage {
  role: Role;
  content: TextContent | ImageContent | AudioContent | EmbeddedResource;
}

export interface EmbeddedResource {
  type: "resource";
  resource: TextResourceContents | BlobResourceContents;
  annotations?: Annotations;
}

export interface PromptListChangedNotification extends Notification {
  method: "notifications/prompts/list_changed";
}

/* Tools */

export interface ListToolsRequest extends PaginatedRequest {
  method: "tools/list";
}

export interface ListToolsResult extends PaginatedResult {
  tools: Tool[];
}

export interface CallToolResult extends Result {
  content: (TextContent | ImageContent | AudioContent | EmbeddedResource)[];
  isError?: boolean;
}

export interface CallToolRequest extends Request {
  method: "tools/call";
  params: {
    name: string;
    arguments?: { [key: string]: unknown };
  };
}

export interface ToolListChangedNotification extends Notification {
  method: "notifications/tools/list_changed";
}

export interface ToolAnnotations {
  title?: string;
  readOnlyHint?: boolean;       // default: false
  destructiveHint?: boolean;    // default: true
  idempotentHint?: boolean;     // default: false
  openWorldHint?: boolean;      // default: true
}

export interface Tool {
  name: string;
  description?: string;
  inputSchema: {
    type: "object";
    properties?: { [key: string]: object };
    required?: string[];
  };
  annotations?: ToolAnnotations;
}

/* Logging */

export interface SetLevelRequest extends Request {
  method: "logging/setLevel";
  params: {
    level: LoggingLevel;
  };
}

export interface LoggingMessageNotification extends Notification {
  method: "notifications/message";
  params: {
    level: LoggingLevel;
    logger?: string;
    data: unknown;
  };
}

export type LoggingLevel =
  | "debug"
  | "info"
  | "notice"
  | "warning"
  | "error"
  | "critical"
  | "alert"
  | "emergency";

/* Sampling */

export interface CreateMessageRequest extends Request {
  method: "sampling/createMessage";
  params: {
    messages: SamplingMessage[];
    modelPreferences?: ModelPreferences;
    systemPrompt?: string;
    includeContext?: "none" | "thisServer" | "allServers";
    temperature?: number;
    maxTokens: number;
    stopSequences?: string[];
    metadata?: object;
  };
}

export interface CreateMessageResult extends Result, SamplingMessage {
  model: string;
  stopReason?: "endTurn" | "stopSequence" | "maxTokens" | string;
}

export interface SamplingMessage {
  role: Role;
  content: TextContent | ImageContent | AudioContent;
}

export interface Annotations {
  audience?: Role[];
  priority?: number; // 0 (least important) to 1 (most important)
}

export interface TextContent {
  type: "text";
  text: string;
  annotations?: Annotations;
}

export interface ImageContent {
  type: "image";
  data: string;   // @format byte (base64)
  mimeType: string;
  annotations?: Annotations;
}

export interface AudioContent {
  type: "audio";
  data: string;   // @format byte (base64)
  mimeType: string;
  annotations?: Annotations;
}

export interface ModelPreferences {
  hints?: ModelHint[];
  costPriority?: number;          // 0-1
  speedPriority?: number;         // 0-1
  intelligencePriority?: number;  // 0-1
}

export interface ModelHint {
  name?: string;
}

/* Autocomplete */

export interface CompleteRequest extends Request {
  method: "completion/complete";
  params: {
    ref: PromptReference | ResourceReference;
    argument: {
      name: string;
      value: string;
    };
  };
}

export interface CompleteResult extends Result {
  completion: {
    values: string[];    // max 100
    total?: number;
    hasMore?: boolean;
  };
}

export interface ResourceReference {
  type: "ref/resource";
  uri: string; // @format uri-template
}

export interface PromptReference {
  type: "ref/prompt";
  name: string;
}

/* Roots */

export interface ListRootsRequest extends Request {
  method: "roots/list";
}

export interface ListRootsResult extends Result {
  roots: Root[];
}

export interface Root {
  uri: string; // @format uri — MUST start with file://
  name?: string;
}

export interface RootsListChangedNotification extends Notification {
  method: "notifications/roots/list_changed";
}

/* Client messages */

export type ClientRequest =
  | PingRequest
  | InitializeRequest
  | CompleteRequest
  | SetLevelRequest
  | GetPromptRequest
  | ListPromptsRequest
  | ListResourcesRequest
  | ListResourceTemplatesRequest
  | ReadResourceRequest
  | SubscribeRequest
  | UnsubscribeRequest
  | CallToolRequest
  | ListToolsRequest;

export type ClientNotification =
  | CancelledNotification
  | ProgressNotification
  | InitializedNotification
  | RootsListChangedNotification;

export type ClientResult =
  | EmptyResult
  | CreateMessageResult
  | ListRootsResult;

/* Server messages */

export type ServerRequest =
  | PingRequest
  | CreateMessageRequest
  | ListRootsRequest;

export type ServerNotification =
  | CancelledNotification
  | ProgressNotification
  | LoggingMessageNotification
  | ResourceUpdatedNotification
  | ResourceListChangedNotification
  | ToolListChangedNotification
  | PromptListChangedNotification;

export type ServerResult =
  | EmptyResult
  | InitializeResult
  | CompleteResult
  | GetPromptResult
  | ListPromptsResult
  | ListResourceTemplatesResult
  | ListResourcesResult
  | ReadResourceResult
  | CallToolResult
  | ListToolsResult;
```

---

## Quick Reference: All JSON-RPC Methods

### Client → Server Requests

| Method | Description |
|--------|-------------|
| `initialize` | Initialize connection, negotiate capabilities |
| `ping` | Check server is alive |
| `resources/list` | List available resources (paginated) |
| `resources/templates/list` | List resource templates (paginated) |
| `resources/read` | Read a specific resource |
| `resources/subscribe` | Subscribe to resource updates |
| `resources/unsubscribe` | Unsubscribe from resource updates |
| `prompts/list` | List available prompts (paginated) |
| `prompts/get` | Get a specific prompt with arguments |
| `tools/list` | List available tools (paginated) |
| `tools/call` | Invoke a tool |
| `completion/complete` | Request autocompletion suggestions |
| `logging/setLevel` | Set minimum log level |

### Server → Client Requests

| Method | Description |
|--------|-------------|
| `ping` | Check client is alive |
| `sampling/createMessage` | Request LLM sampling |
| `roots/list` | Request filesystem roots |

### Client → Server Notifications

| Method | Description |
|--------|-------------|
| `notifications/initialized` | Client ready after initialization |
| `notifications/cancelled` | Cancel an in-progress request |
| `notifications/progress` | Progress update for a request |
| `notifications/roots/list_changed` | Roots list has changed |

### Server → Client Notifications

| Method | Description |
|--------|-------------|
| `notifications/cancelled` | Cancel an in-progress request |
| `notifications/progress` | Progress update for a request |
| `notifications/message` | Log message |
| `notifications/resources/list_changed` | Resource list changed |
| `notifications/resources/updated` | Specific resource updated |
| `notifications/prompts/list_changed` | Prompt list changed |
| `notifications/tools/list_changed` | Tool list changed |
