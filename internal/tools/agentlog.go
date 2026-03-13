package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// DevLogPath is the absolute path to the agentic output log during development.
const DevLogPath = "/Users/home/Documents/Code/Go_dev/nostop/agent.log"

// AgentLogger writes structured agentic events to a log file.
// All tool calls, results, and loop iterations are recorded.
type AgentLogger struct {
	mu   sync.Mutex
	file *os.File
}

// agentLog is the package-level logger instance, initialized lazily.
var (
	agentLog     *AgentLogger
	agentLogOnce sync.Once
)

// GetAgentLogger returns the singleton agent logger, creating the log file if needed.
func GetAgentLogger() *AgentLogger {
	agentLogOnce.Do(func() {
		f, err := os.OpenFile(DevLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to open agent log %s: %s\n", DevLogPath, err)
			return
		}
		agentLog = &AgentLogger{file: f}
		agentLog.Log("session_start", map[string]any{
			"pid": os.Getpid(),
		})
	})
	return agentLog
}

// Log writes a structured event to the log file.
func (l *AgentLogger) Log(event string, data map[string]any) {
	if l == nil || l.file == nil {
		return
	}

	entry := map[string]any{
		"time":  time.Now().Format(time.RFC3339Nano),
		"event": event,
	}
	for k, v := range data {
		entry[k] = v
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.file.Write(raw)
	l.file.Write([]byte("\n"))
}

// LogToolCall records a tool invocation.
func (l *AgentLogger) LogToolCall(name, id string, input map[string]any, iteration int) {
	l.Log("tool_call", map[string]any{
		"tool":      name,
		"id":        id,
		"input":     input,
		"iteration": iteration,
	})
}

// LogToolResult records a tool's output.
func (l *AgentLogger) LogToolResult(name, id string, output string, isError bool, elapsed time.Duration) {
	// Truncate output for the log
	logOutput := output
	if len(logOutput) > 2000 {
		logOutput = logOutput[:2000] + "... (truncated in log)"
	}

	l.Log("tool_result", map[string]any{
		"tool":     name,
		"id":       id,
		"output":   logOutput,
		"is_error": isError,
		"elapsed":  elapsed.String(),
	})
}

// LogIteration records the start of an agentic loop iteration.
func (l *AgentLogger) LogIteration(iteration int, messageCount int) {
	l.Log("iteration", map[string]any{
		"iteration":     iteration,
		"message_count": messageCount,
	})
}

// LogResponse records the final response from Claude.
func (l *AgentLogger) LogResponse(iterations int, hasToolUse bool, textLength int) {
	l.Log("response", map[string]any{
		"iterations":   iterations,
		"has_tool_use": hasToolUse,
		"text_length":  textLength,
	})
}

// Close flushes and closes the log file.
func (l *AgentLogger) Close() {
	if l == nil || l.file == nil {
		return
	}
	l.Log("session_end", nil)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.file.Close()
}
