# RLM HQ Delegation Plan

## Overview

This plan coordinates multiple Claude agents to implement the RLM system in parallel using the `concurrent-agent-mcp` (HQ) tool.

**Project Name**: `rlm-build`
**Total Steps**: 20
**Estimated Agent Parallelism**: Up to 4 concurrent agents

## Dependency Graph

```
Step 1 (go.mod)
    │
    ├──────────────────────────────────────┐
    │                                      │
    v                                      v
Step 2 (API types)                    Step 5 (SQLite storage)
    │                                      │
    ├─────────────┐                        │
    │             │                        │
    v             v                        │
Step 3 (client)  Step 4 (stream)           │
    │                                      │
    └──────────────────────────────────────┤
                                           │
    ┌──────────────────────────────────────┼─────────────────────┐
    │                                      │                     │
    v                                      v                     │
Step 6 (detector)                     Step 7 (tracker)          │
    │                                      │                     │
    └──────────────┬───────────────────────┤                     │
                   │                       │                     │
                   v                       │                     │
              Step 8 (scorer)              │                     │
                   │                       │                     │
                   └───────────────────────┼─────────────────────┤
                                           │                     │
                        ┌──────────────────┴────────┐            │
                        │                           │            │
                        v                           v            │
                   Step 9 (ctx mgr)           Step 10 (archiver) │
                        │                           │            │
                        └───────────┬───────────────┘            │
                                    │                            │
                                    v                            │
                              Step 11 (RLM engine) <─────────────┘
                                    │
                                    v
                              Step 12 (TUI app)
                                    │
          ┌─────────────┬───────────┼───────────┬────────────────┐
          │             │           │           │                │
          v             v           v           v                │
    Step 13 (chat) Step 14 (hist) Step 15 (topics) Step 16 (debug)
          │             │           │           │                │
          └─────────────┴───────────┴───────────┴────────────────┤
                                                                 │
                              ┌───────────────────────────────────┘
                              │
    ┌─────────────┬───────────┼───────────┬────────────────┐
    │             │           │           │                │
    v             v           v           v                │
Step 17 (cache) Step 18 (restore) Step 19 (config) Step 20 (errors)
```

## Step Definitions

### Phase 1: Core Foundation

| Step | Branch | Scope | Depends | Description |
|------|--------|-------|---------|-------------|
| 1 | `feature/go-mod` | foundation | - | Create go.mod, project structure, directories |
| 2 | `feature/api-types` | api | 1 | API types (Message, ContentBlock, Request, Response) |
| 3 | `feature/api-client` | api | 2 | Claude API client with Messages + token counting |
| 4 | `feature/api-stream` | api | 2 | SSE streaming handler for responses |
| 5 | `feature/storage` | storage | 1 | SQLite storage, schema, models, migrations |

### Phase 2: Topic System

| Step | Branch | Scope | Depends | Description |
|------|--------|-------|---------|-------------|
| 6 | `feature/topic-detector` | topic | 3 | Claude-based topic detection using Haiku |
| 7 | `feature/topic-tracker` | topic | 5 | Topic state management, allocation tracking |
| 8 | `feature/topic-scorer` | topic | 6, 7 | Relevance scoring via Claude |

### Phase 3: Context Management

| Step | Branch | Scope | Depends | Description |
|------|--------|-------|---------|-------------|
| 9 | `feature/context-manager` | rlm | 7 | Context usage tracking, budget calculations |
| 10 | `feature/archiver` | rlm | 5, 7 | Archive/restore logic for topics |
| 11 | `feature/rlm-engine` | rlm | 3, 6, 7, 8, 9, 10 | Main RLM orchestrator |

### Phase 4: CLI

| Step | Branch | Scope | Depends | Description |
|------|--------|-------|---------|-------------|
| 12 | `feature/tui-app` | tui | 11 | Bubbletea app scaffolding, main model |
| 13 | `feature/tui-chat` | tui | 4, 12 | Chat view with streaming display |
| 14 | `feature/tui-history` | tui | 5, 12 | Conversation history browser |
| 15 | `feature/tui-topics` | tui | 7, 12 | Topics overview view |
| 16 | `feature/tui-debug` | tui | 9, 12 | Debug/context info view |

### Phase 5: Polish

| Step | Branch | Scope | Depends | Description |
|------|--------|-------|---------|-------------|
| 17 | `feature/cache-control` | polish | 3 | Cache control headers for system prompts |
| 18 | `feature/topic-restore-ui` | polish | 10, 15 | Topic restoration UI integration |
| 19 | `feature/config` | polish | 11 | Configuration file support (TOML) |
| 20 | `feature/error-handling` | polish | 3, 11 | Graceful error handling and retries |

## Parallelization Waves

Based on dependencies, agents can work in these waves:

### Wave 1 (Bootstrap)
- **Step 1** (go.mod) - Single agent, required first

### Wave 2 (Foundation - 2 parallel)
- **Step 2** (API types)
- **Step 5** (SQLite storage) - Can run in parallel!

### Wave 3 (API Completion - 2 parallel)
- **Step 3** (API client)
- **Step 4** (SSE streaming) - Can run in parallel!

### Wave 4 (Topic Foundation - 2 parallel)
- **Step 6** (detector) - Needs client
- **Step 7** (tracker) - Needs storage, can run in parallel!

### Wave 5 (Topic Completion)
- **Step 8** (scorer) - Needs detector + tracker

### Wave 6 (Context - 2 parallel)
- **Step 9** (context manager)
- **Step 10** (archiver) - Can run in parallel!

### Wave 7 (Engine)
- **Step 11** (RLM engine) - Integrates everything

### Wave 8 (TUI Foundation)
- **Step 12** (TUI app) - Entry point

### Wave 9 (TUI Views - 4 parallel)
- **Step 13** (chat)
- **Step 14** (history)
- **Step 15** (topics)
- **Step 16** (debug) - All can run in parallel!

### Wave 10 (Polish - 4 parallel)
- **Step 17** (cache control)
- **Step 18** (restore UI)
- **Step 19** (config)
- **Step 20** (error handling) - All can run in parallel!

## Agent Instructions Template

Each agent should:

1. **Read context files first**:
   ```
   - START.md (project overview)
   - RLM_PLAN.md (full implementation details)
   - API_REFERENCE.md (Claude API docs)
   ```

2. **Claim step via HQ**:
   ```
   mcp__hq__claim_step(project="rlm-build", agent_id="agent-N")
   ```

3. **Start work and heartbeat**:
   ```
   mcp__hq__start_step(step_id=X)
   mcp__hq__heartbeat(step_id=X, agent_id="agent-N") every 30-60s
   ```

4. **Complete with commit**:
   ```
   mcp__hq__complete_step(step_id=X, commit_hash="abc123", files_modified=["..."])
   ```

## HQ Project Creation JSON

```json
{
  "name": "rlm-build",
  "base_commit": "<current HEAD>",
  "steps": [
    {"step_num": 1, "branch": "feature/go-mod", "scope": "foundation", "depends_on": []},
    {"step_num": 2, "branch": "feature/api-types", "scope": "api", "depends_on": [1]},
    {"step_num": 3, "branch": "feature/api-client", "scope": "api", "depends_on": [2]},
    {"step_num": 4, "branch": "feature/api-stream", "scope": "api", "depends_on": [2]},
    {"step_num": 5, "branch": "feature/storage", "scope": "storage", "depends_on": [1]},
    {"step_num": 6, "branch": "feature/topic-detector", "scope": "topic", "depends_on": [3]},
    {"step_num": 7, "branch": "feature/topic-tracker", "scope": "topic", "depends_on": [5]},
    {"step_num": 8, "branch": "feature/topic-scorer", "scope": "topic", "depends_on": [6, 7]},
    {"step_num": 9, "branch": "feature/context-manager", "scope": "rlm", "depends_on": [7]},
    {"step_num": 10, "branch": "feature/archiver", "scope": "rlm", "depends_on": [5, 7]},
    {"step_num": 11, "branch": "feature/rlm-engine", "scope": "rlm", "depends_on": [3, 6, 7, 8, 9, 10]},
    {"step_num": 12, "branch": "feature/tui-app", "scope": "tui", "depends_on": [11]},
    {"step_num": 13, "branch": "feature/tui-chat", "scope": "tui", "depends_on": [4, 12]},
    {"step_num": 14, "branch": "feature/tui-history", "scope": "tui", "depends_on": [5, 12]},
    {"step_num": 15, "branch": "feature/tui-topics", "scope": "tui", "depends_on": [7, 12]},
    {"step_num": 16, "branch": "feature/tui-debug", "scope": "tui", "depends_on": [9, 12]},
    {"step_num": 17, "branch": "feature/cache-control", "scope": "polish", "depends_on": [3]},
    {"step_num": 18, "branch": "feature/topic-restore-ui", "scope": "polish", "depends_on": [10, 15]},
    {"step_num": 19, "branch": "feature/config", "scope": "polish", "depends_on": [11]},
    {"step_num": 20, "branch": "feature/error-handling", "scope": "polish", "depends_on": [3, 11]}
  ]
}
```

## Recommended Execution Strategy

### Option A: Sequential Phases (Safer)
Run each phase to completion before starting the next:
- Easier merge conflicts
- Better for unfamiliar codebase
- 5 main phases, ~10 waves total

### Option B: Maximum Parallelism (Faster)
Launch agents as soon as dependencies are satisfied:
- Up to 4 concurrent agents
- More merge complexity
- ~60% faster completion

### Option C: Hybrid (Recommended)
- Phase 1 (Foundation): Sequential - establish patterns
- Phases 2-3: 2 parallel agents
- Phase 4 (TUI): 4 parallel agents (independent views)
- Phase 5: 4 parallel agents (independent polish)

## Pre-Requisites

Before creating the HQ project:

1. [ ] Initialize git repository if not done
2. [ ] Create initial commit with planning docs
3. [ ] Get current HEAD commit hash for `base_commit`

## Notes

- Agents should NOT modify files outside their scope
- Each agent works in a feature branch
- Merges happen after step completion
- Heartbeats every 30-60 seconds prevent stale detection
- If agent crashes, use `recover_step` to reset

## Reference Files

- `START.md` - Quick start guide
- `RLM_PLAN.md` - Full implementation plan
- `API_REFERENCE.md` - Claude API documentation
