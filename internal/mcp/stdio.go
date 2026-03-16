package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
)

// StdioTransport communicates with an MCP server subprocess via stdin/stdout.
//
// Goroutine structure:
//  1. Main goroutine: Owns stdin writer. Send() writes request, blocks on response channel.
//  2. Reader goroutine: Reads stdout line-by-line, routes responses to pending requests by ID.
//  3. Stderr goroutine: Drains stderr to debug log, preventing pipe buffer fill (64KB on macOS).
//  4. Process watcher: Calls cmd.Wait(), then cleans up on exit.
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	// pending maps request IDs to response channels.
	pendingMu sync.Mutex
	pending   map[string]chan *Response

	// closed is set to true when Close() is called.
	closed atomic.Bool

	// wg tracks the reader, stderr, and watcher goroutines.
	wg sync.WaitGroup

	// err holds the process exit error for diagnostics.
	errMu sync.Mutex
	err   error
}

// NewStdioTransport creates a new StdioTransport by starting the given command.
// The command is started immediately. Call Close() to kill the subprocess.
func NewStdioTransport(command string, args []string, env []string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start MCP server %q: %w", command, err)
	}

	t := &StdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		pending: make(map[string]chan *Response),
	}

	t.wg.Add(3)
	go t.readLoop()
	go t.stderrLoop()
	go t.watchProcess()

	return t, nil
}

// Send sends a JSON-RPC request and waits for the matching response.
func (t *StdioTransport) Send(ctx context.Context, req *Request) (*Response, error) {
	if t.closed.Load() {
		return nil, fmt.Errorf("transport is closed")
	}

	data, err := MarshalRequest(req)
	if err != nil {
		return nil, err
	}

	// Create a response channel keyed by the string form of the request ID.
	idKey := idToString(req.ID)
	ch := make(chan *Response, 1)

	t.pendingMu.Lock()
	t.pending[idKey] = ch
	t.pendingMu.Unlock()

	defer func() {
		t.pendingMu.Lock()
		delete(t.pending, idKey)
		t.pendingMu.Unlock()
	}()

	// Write request to stdin.
	if _, writeErr := t.stdin.Write(data); writeErr != nil {
		return nil, fmt.Errorf("failed to write to MCP server stdin: %w", writeErr)
	}

	// Wait for response or context cancellation.
	select {
	case resp := <-ch:
		if resp == nil {
			return nil, fmt.Errorf("MCP server process exited while waiting for response")
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (t *StdioTransport) Notify(ctx context.Context, notif *Notification) error {
	if t.closed.Load() {
		return fmt.Errorf("transport is closed")
	}

	data, err := MarshalNotification(notif)
	if err != nil {
		return err
	}

	if _, writeErr := t.stdin.Write(data); writeErr != nil {
		return fmt.Errorf("failed to write notification to MCP server stdin: %w", writeErr)
	}
	return nil
}

// Close kills the subprocess and waits for all goroutines to exit.
func (t *StdioTransport) Close() error {
	if t.closed.Swap(true) {
		return nil // already closed
	}

	// Kill the subprocess. The process watcher goroutine will detect exit,
	// close stdout (unblocking reader), and drain pending channels.
	if t.cmd.Process != nil {
		t.cmd.Process.Kill()
	}

	// Wait for all goroutines to finish.
	t.wg.Wait()
	return nil
}

// Err returns the process exit error, if any.
func (t *StdioTransport) Err() error {
	t.errMu.Lock()
	defer t.errMu.Unlock()
	return t.err
}

// readLoop reads JSON-RPC responses from stdout line-by-line and routes
// them to the matching pending request channel.
func (t *StdioTransport) readLoop() {
	defer t.wg.Done()

	scanner := bufio.NewScanner(t.stdout)
	// MCP responses can be large (tool results with file contents).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			log.Printf("[mcp] failed to parse response: %v (line: %s)", err, string(line))
			continue
		}

		idKey := idToString(resp.ID)
		t.pendingMu.Lock()
		ch, ok := t.pending[idKey]
		t.pendingMu.Unlock()

		if ok {
			ch <- &resp
		} else {
			// Server-initiated message (notification) — log for now.
			log.Printf("[mcp] received unmatched response/notification: method or id=%v", resp.ID)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[mcp] stdout reader error: %v", err)
	}

	// Stdout closed (process exited or pipe broken).
	// Drain all pending requests with nil to signal error.
	t.drainPending()
}

// stderrLoop drains stderr to the debug log, preventing pipe buffer fill.
func (t *StdioTransport) stderrLoop() {
	defer t.wg.Done()

	scanner := bufio.NewScanner(t.stderr)
	for scanner.Scan() {
		log.Printf("[mcp:stderr] %s", scanner.Text())
	}
}

// watchProcess waits for the subprocess to exit and cleans up.
func (t *StdioTransport) watchProcess() {
	defer t.wg.Done()

	err := t.cmd.Wait()

	t.errMu.Lock()
	t.err = err
	t.errMu.Unlock()

	// Close stdout to unblock the reader goroutine.
	t.stdout.Close()
}

// drainPending sends nil to all pending response channels to unblock
// any goroutines waiting in Send().
func (t *StdioTransport) drainPending() {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()

	for id, ch := range t.pending {
		ch <- nil
		delete(t.pending, id)
	}
}

// idToString converts a JSON-RPC ID (any type) to a consistent string key
// for use in the pending map.
func idToString(id any) string {
	switch v := id.(type) {
	case string:
		return "s:" + v
	case float64:
		return fmt.Sprintf("n:%g", v)
	case int64:
		return fmt.Sprintf("n:%d", v)
	case int:
		return fmt.Sprintf("n:%d", v)
	case nil:
		return "null"
	default:
		return fmt.Sprintf("?:%v", v)
	}
}
