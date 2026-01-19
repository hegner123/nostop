# RLM Project - Quick Start

## Key Files to Memorize

**Read these files at the start of every session:**

1. **RLM_PLAN.md** - Complete implementation plan with architecture, types, schema, and phases
2. **API_REFERENCE.md** - Full Claude API documentation (Messages API, token counting, streaming, cache control)

## Project Overview

RLM (Recursive Language Model) is a Go library + CLI for intelligent **topic-based context archival** before sending messages to Claude.

**Core principle**: Archive, don't compact. Full messages are preserved in SQLite, not summarized.

## Architecture Summary

```
rlm/
├── cmd/rlm/           # CLI entry point (Bubbletea)
├── internal/
│   ├── api/           # Claude API client, types, streaming
│   ├── storage/       # SQLite operations, schema, models
│   ├── topic/         # Topic detection, tracking, scoring
│   └── tui/           # Bubbletea views (chat, history, topics)
└── pkg/rlm/           # Main RLM engine (public API)
```

## Key Concepts

- **Topic Detection**: Claude (Haiku) identifies conversation topics
- **Dynamic Allocation**: Current topics get more context budget, older topics get less
- **Archival Trigger**: At 95% context usage, archive until 50% free
- **No Summarization**: Full messages preserved in SQLite

## Dependencies

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/charmbracelet/lipgloss` - Styling
- `github.com/charmbracelet/bubbles` - TUI components
- `modernc.org/sqlite` - Pure Go SQLite (no CGO)

## Implementation Phases

1. **Core Foundation** - API types, client, streaming, SQLite
2. **Topic System** - Detection, tracking, scoring
3. **Context Management** - Usage tracking, archival logic
4. **CLI** - Bubbletea views
5. **Polish** - Cache control, config, error handling

## Current Status

Project is in planning phase. See RLM_PLAN.md for full implementation checklist.

## Quick Commands

```bash
# Once implemented:
go run ./cmd/rlm          # Start CLI
go test ./...             # Run tests
```
