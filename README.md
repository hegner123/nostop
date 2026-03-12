# rlm

A Go CLI and library for intelligent topic-based context management when chatting with Claude. Instead of naively truncating conversation history when approaching token limits, RLM detects topic shifts, scores relevance, and archives low-relevance topics to SQLite — preserving full message content for later restoration.

## Features

- **Topic detection** — Uses Claude Haiku as a sidecar model to identify conversation topic shifts in real time
- **Dynamic context allocation** — Current topics receive more context budget; older topics receive proportionally less based on relevance scoring
- **Archive, don't compact** — Full messages are preserved verbatim in SQLite, not summarized or truncated
- **Automatic archival** — At 95% context usage, archives lowest-relevance topics until usage drops to 50%
- **Topic restoration** — Archived topics can be brought back into active context when referenced
- **Prompt caching** — Integrates with Anthropic's cache control API (5m and 1h TTLs) to reduce costs on repeated system prompts
- **Streaming** — Full SSE streaming support with callback-based API
- **TUI** — Four-view Bubbletea interface: Chat, History, Topics, and Debug

## Prerequisites

- Go 1.24+
- An [Anthropic API key](https://console.anthropic.com/)

## Installation

```bash
go install github.com/user/rlm/cmd/rlm@latest
```

Build from source:

```bash
git clone https://github.com/user/rlm.git
cd rlm
just build
just install
```

## Usage

Set your API key:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

Start the TUI:

```bash
rlm
```

Generate a default config file:

```bash
rlm --write-config
```

### Configuration

RLM searches for config files in order:

1. `./rlm.toml`
2. `~/.config/rlm/config.toml`
3. `~/.rlm.toml`

Key settings in `rlm.toml`:

```toml
[context]
max_tokens = 200000        # Model context limit
archive_threshold = 0.95   # Trigger archival at this usage %
archive_target = 0.50      # Archive until usage drops to this %

[models]
chat = "claude-sonnet-4-20250514"
detection = "claude-3-5-haiku-latest"
```

### Key Bindings

| Key | Action |
|-----|--------|
| `ctrl+n` | New conversation |
| `ctrl+h` | History view |
| `ctrl+t` | Topics view |
| `ctrl+d` | Debug view |
| `tab` / `shift+tab` | Cycle views |
| `r` (Topics view) | Restore archived topic |

### Library Usage

RLM can be used as a Go library:

```go
engine, err := rlm.New(rlm.Config{
    APIKey:           os.Getenv("ANTHROPIC_API_KEY"),
    DBPath:           "conversations.db",
    MaxContextTokens: 200000,
    ChatModel:        "claude-sonnet-4-20250514",
    DetectionModel:   "claude-3-5-haiku-latest",
})
defer engine.Close()

conv, _ := engine.NewConversation(ctx, "My Chat", "You are helpful.")
resp, _ := engine.Send(ctx, conv.ID, "Hello!")
fmt.Println(resp.Content)
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. See [LICENSE](LICENSE).
