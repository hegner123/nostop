// Package tui provides the Bubbletea-based terminal user interface for Nostop.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hegner123/nostop/internal/api"
	"github.com/hegner123/nostop/internal/storage"
	"github.com/hegner123/nostop/pkg/nostop"
)

// ChatMessage represents a message displayed in the chat view.
type ChatMessage struct {
	Role       string // "user", "assistant", "system", or "tool"
	Content    string
	Topic      string // topic name when message was sent
	ToolName   string // populated when Role == "tool"
	ToolTarget string // file/dir the tool is operating on (for display)
	IsError    bool   // tool execution error
}

// ChatModel is the Bubbletea model for the chat view.
type ChatModel struct {
	engine    *nostop.Nostop
	archiver  *nostop.Archiver
	convID    string
	messages  []ChatMessage
	input     textarea.Model
	viewport  viewport.Model
	streaming bool
	streamBuf strings.Builder
	width     int
	height    int
	err       error
	styles    Styles
	ready     bool
	topicName string // current topic name

	// Restore prompt integration
	restorePrompt  *RestorePrompt
	pendingMessage string // Message waiting for restore decision
	notifications  *NotificationManager

	// Stream channel — per-model, not global — to avoid cross-talk
	// when rapid sends overwrite a shared channel.
	streamCh chan tea.Msg

	// ctx is the cancellable context propagated from the App root.
	ctx context.Context
}

// Stream message types for Bubbletea

// StreamStartMsg indicates streaming has started.
type StreamStartMsg struct{}

// StreamChunkMsg contains a chunk of streamed text.
type StreamChunkMsg struct {
	Text string
}

// StreamDoneMsg indicates streaming completed successfully.
type StreamDoneMsg struct {
	Response *nostop.Response
}

// StreamErrorMsg indicates an error during streaming.
type StreamErrorMsg struct {
	Err error
}

// ToolCallMsg indicates a tool invocation has started.
type ToolCallMsg struct {
	Name  string
	ID    string
	Input map[string]any
}

// ToolResultMsg indicates a tool invocation has completed.
type ToolResultMsg struct {
	Name    string
	Output  string
	IsError bool
}

// ConversationCreatedMsg indicates a new conversation was created.
type ConversationCreatedMsg struct {
	ConvID string
	Title  string
}

// conversationCreatedWithContentMsg indicates a conversation was created with pending content to send.
type conversationCreatedWithContentMsg struct {
	ConvID  string
	Title   string
	Content string
}

// NewChatModel creates a new ChatModel with the given Nostop engine and dimensions.
func NewChatModel(engine *nostop.Nostop, ctx context.Context, width, height int) ChatModel {
	// Create textarea for input
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Enter to send, Shift+Enter for newline)"
	ta.Focus()
	ta.CharLimit = 0 // No character limit
	ta.ShowLineNumbers = false

	// Set textarea dimensions.
	// Input style has border (2 cols) + padding (2 cols) = 4 cols decoration.
	// Width(w-6) is the outer width, so content area = w-6-4 = w-10.
	inputHeight := 3
	ta.SetWidth(width - 10)
	ta.SetHeight(inputHeight)

	// Create viewport for message history
	// Reserve space for input area and some padding
	vpHeight := height - inputHeight - 4
	if vpHeight < 5 {
		vpHeight = 5
	}
	vp := viewport.New(width-2, vpHeight)
	vp.SetContent("")

	return ChatModel{
		engine:        engine,
		ctx:           ctx,
		input:         ta,
		viewport:      vp,
		messages:      make([]ChatMessage, 0),
		width:         width,
		height:        height,
		styles:        DefaultStyles(),
		ready:         true,
		restorePrompt: NewRestorePrompt(nil, "", width),
		notifications: NewNotificationManager(),
	}
}

// SetArchiver sets the archiver for restore detection.
func (m *ChatModel) SetArchiver(archiver *nostop.Archiver) {
	m.archiver = archiver
}

// Init implements tea.Model.
func (m *ChatModel) Init() tea.Cmd {
	return textarea.Blink
}

// Update implements tea.Model.
func (m *ChatModel) Update(msg tea.Msg) (*ChatModel, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		Log("ChatModel.Update KeyMsg: key=%q type=%v streaming=%v convID=%q", msg.String(), msg.Type, m.streaming, m.convID)
		// Don't process keys while streaming
		if m.streaming {
			Log("ChatModel: ignoring key during streaming")
			return m, nil
		}

		// If restore prompt is visible, let it handle keys first
		if m.restorePrompt != nil && m.restorePrompt.IsVisible() {
			restore, dismiss, handled := m.restorePrompt.HandleKey(msg.String())
			if handled {
				if restore {
					// User chose to restore - trigger restoration
					topic := m.restorePrompt.GetSelectedTopic()
					pendingMsg := m.pendingMessage
					m.restorePrompt.Clear()
					m.pendingMessage = ""
					return m, m.restoreAndSend(topic, pendingMsg)
				}
				if dismiss {
					// User chose to skip - just send the message
					pendingMsg := m.pendingMessage
					m.restorePrompt.Clear()
					m.pendingMessage = ""
					if pendingMsg != "" {
						return m.sendMessageDirectly(pendingMsg)
					}
				}
				return m, nil
			}
		}

		switch msg.Type {
		case tea.KeyEnter:
			Log("ChatModel: Enter pressed, Alt=%v", msg.Alt)
			// Check if Shift is held for newline
			if msg.Alt {
				// Let textarea handle Shift+Enter as newline
				m.input, tiCmd = m.input.Update(msg)
				return m, tiCmd
			}
			// Send message (with restore check)
			Log("ChatModel: calling handleSendMessage")
			return m.handleSendMessage()

		case tea.KeyEsc:
			// Clear restore prompt if visible
			if m.restorePrompt != nil && m.restorePrompt.IsVisible() {
				m.restorePrompt.Clear()
				m.pendingMessage = ""
				return m, nil
			}
			// Blur the input
			m.input.Blur()
			return m, nil

		case tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
			// Route navigation keys to viewport for scrolling
			m.viewport, vpCmd = m.viewport.Update(msg)
			return m, vpCmd

		case tea.KeyUp:
			// Up at first line of input scrolls viewport; otherwise move cursor
			if m.input.Line() == 0 {
				m.viewport, vpCmd = m.viewport.Update(msg)
				return m, vpCmd
			}
			m.input, tiCmd = m.input.Update(msg)
			return m, tiCmd

		case tea.KeyDown:
			// Down at last line of input scrolls viewport; otherwise move cursor
			if m.input.Line() == m.input.LineCount()-1 {
				m.viewport, vpCmd = m.viewport.Update(msg)
				return m, vpCmd
			}
			m.input, tiCmd = m.input.Update(msg)
			return m, tiCmd

		default:
			// Pass other keys to textarea
			m.input, tiCmd = m.input.Update(msg)
			return m, tiCmd
		}

	case TopicRestoreCompleteMsg:
		// Handle completion of topic restoration
		if msg.Error != nil {
			m.err = msg.Error
			return m, nil
		}
		if msg.Topic != nil {
			// Add notification
			m.notifications.AddRestoreNotification(msg.Topic.Name, msg.Topic.TokenCount)
			m.updateViewport()
		}
		return m, nil

	case RestoreCheckResultMsg:
		// Handle result of restore check
		if len(msg.Topics) > 0 {
			// Show restore prompt
			m.restorePrompt.SetTopics(msg.Topics, msg.Message)
			m.restorePrompt.SetWidth(m.width - 10)
			m.pendingMessage = msg.Message
		} else {
			// No archived topics matched, send directly
			return m.sendMessageDirectly(msg.Message)
		}
		return m, nil

	case RestoreAndSendMsg:
		// Topic was restored, now send the message
		if msg.RestoredTopic != nil {
			m.notifications.AddRestoreNotification(msg.RestoredTopic.Name, msg.RestoredTopic.TokenCount)
		}
		// Send the original message
		return m.sendMessageDirectly(msg.Message)

	case ToolCallMsg:
		// Finalize any in-progress assistant message so intermediate
		// reasoning is preserved (e.g. "Let me check that file...").
		// Only remove the placeholder if it's truly empty.
		if len(m.messages) > 0 {
			last := m.messages[len(m.messages)-1]
			if last.Role == "assistant" && m.streaming {
				if strings.TrimSpace(last.Content) == "" {
					// Empty placeholder — remove it
					m.messages = m.messages[:len(m.messages)-1]
				}
				// Non-empty assistant text is kept as-is
				m.streamBuf.Reset()
			}
		}
		m.streaming = false

		target := toolTarget(msg.Input)
		m.messages = append(m.messages, ChatMessage{
			Role:       "tool",
			ToolName:   msg.Name,
			ToolTarget: target,
			Content:    "...",
		})
		m.updateViewport()
		m.viewport.GotoBottom()
		return m, m.waitForStream()

	case ToolResultMsg:
		// Update the last tool message with the result
		for i := len(m.messages) - 1; i >= 0; i-- {
			if m.messages[i].Role == "tool" && m.messages[i].ToolName == msg.Name {
				m.messages[i].Content = formatToolOutput(msg.Output, 500)
				m.messages[i].IsError = msg.IsError
				break
			}
		}
		m.updateViewport()
		m.viewport.GotoBottom()
		return m, m.waitForStream()

	case StreamStartMsg:
		Log("StreamStartMsg received - starting streaming")
		m.streaming = true
		m.streamBuf.Reset()
		// Add placeholder for assistant message
		m.messages = append(m.messages, ChatMessage{
			Role:    "assistant",
			Content: "",
			Topic:   m.topicName,
		})
		m.updateViewport()
		// Continue reading from stream channel
		return m, m.waitForStream()

	case StreamChunkMsg:
		// If we're not streaming (e.g. tools just finished and a new
		// iteration started), create a fresh assistant placeholder.
		if !m.streaming {
			m.streaming = true
			m.streamBuf.Reset()
			m.messages = append(m.messages, ChatMessage{
				Role:    "assistant",
				Content: "",
				Topic:   m.topicName,
			})
		}

		if len(m.messages) > 0 {
			m.streamBuf.WriteString(msg.Text)
			m.messages[len(m.messages)-1].Content = m.streamBuf.String()
			m.updateViewport()
			m.viewport.GotoBottom()
		}
		// Continue reading from stream channel
		return m, m.waitForStream()

	case StreamDoneMsg:
		Log("StreamDoneMsg received - streaming complete")
		m.streaming = false
		if msg.Response != nil && len(m.messages) > 0 {
			// Find the last assistant message to update with final content.
			// Do NOT blindly overwrite the last message — it may be a tool result.
			for i := len(m.messages) - 1; i >= 0; i-- {
				if m.messages[i].Role == "assistant" {
					// Only update if the final response has content
					if msg.Response.Content != "" {
						m.messages[i].Content = msg.Response.Content
					}
					break
				}
			}
			// Check for topic shift
			if msg.Response.TopicShift != nil && msg.Response.TopicShift.Detected {
				oldTopic := m.topicName
				m.topicName = msg.Response.TopicShift.NewTopicName
				if m.notifications != nil {
					if oldTopic == "" {
						m.notifications.AddTopicNotification(m.topicName, false)
					} else {
						m.notifications.AddTopicNotification(m.topicName, true)
					}
				}
			}
		}
		m.updateViewport()
		m.viewport.GotoBottom()
		// Streaming done - no more commands needed
		return m, nil

	case StreamErrorMsg:
		Log("StreamErrorMsg received: %v", msg.Err)
		m.streaming = false
		m.err = msg.Err
		// Remove the placeholder assistant message if streaming failed
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == "assistant" && m.messages[len(m.messages)-1].Content == "" {
			m.messages = m.messages[:len(m.messages)-1]
		}
		// Add a system message with user-friendly error info
		userMsg := nostop.UserFriendlyMessage(msg.Err)
		action := nostop.SuggestAction(msg.Err)
		if action != nostop.ActionNone {
			userMsg = userMsg + " " + nostop.ActionMessage(action)
		}
		m.messages = append(m.messages, ChatMessage{
			Role:    "system",
			Content: userMsg,
			Topic:   m.topicName,
		})
		m.updateViewport()
		return m, nil

	case ConversationCreatedMsg:
		Log("ConversationCreatedMsg: convID=%q", msg.ConvID)
		m.convID = msg.ConvID
		m.messages = nil
		m.topicName = ""
		m.updateViewport()
		return m, nil

	case conversationCreatedWithContentMsg:
		// Conversation was created with pending content - now send the message
		Log("conversationCreatedWithContentMsg: convID=%q content=%q", msg.ConvID, msg.Content)
		m.convID = msg.ConvID
		m.messages = nil
		m.topicName = ""
		// Now send the pending message
		return m.sendMessageDirectly(msg.Content)

	default:
		// Pass other messages to textarea and viewport
		m.input, tiCmd = m.input.Update(msg)
		m.viewport, vpCmd = m.viewport.Update(msg)
		return m, tea.Batch(tiCmd, vpCmd)
	}
}

// View implements tea.Model.
func (m ChatModel) View() string {
	var b strings.Builder

	// Show topic indicator if we have a topic
	if m.topicName != "" {
		topicLabel := m.styles.TopicLabel.Render("Topic:")
		topicName := m.styles.TopicCurrent.Render(" " + m.topicName)
		b.WriteString(topicLabel + topicName)
		b.WriteString("\n\n")
	}

	// Show restore notifications
	if m.notifications != nil && m.notifications.HasNotifications() {
		b.WriteString(m.notifications.View())
		b.WriteString("\n")
	}

	// Render message viewport
	b.WriteString(m.viewport.View())
	b.WriteString("\n\n")

	// Show restore prompt if visible
	if m.restorePrompt != nil && m.restorePrompt.IsVisible() {
		b.WriteString(m.restorePrompt.View())
		b.WriteString("\n\n")
	}

	// Show streaming indicator
	if m.streaming {
		b.WriteString(m.styles.Info.Render("Streaming response..."))
		b.WriteString("\n")
	}

	// Render input area
	inputStyle := m.styles.Input.Width(m.width - 6)
	b.WriteString(inputStyle.Render(m.input.View()))

	return b.String()
}

// handleSendMessage processes sending a message.
func (m *ChatModel) handleSendMessage() (*ChatModel, tea.Cmd) {
	content := strings.TrimSpace(m.input.Value())
	Log("handleSendMessage: content=%q convID=%q archiver=%v engine=%v", content, m.convID, m.archiver != nil, m.engine != nil)
	if content == "" {
		Log("handleSendMessage: empty content, ignoring")
		return m, nil
	}

	// Vim-style quit commands
	if content == ":q" || content == ":quit" || content == ":exit" {
		m.input.Reset()
		return m, tea.Quit
	}

	// Check if we have a conversation - create one if not
	if m.convID == "" {
		Log("handleSendMessage: no conversation ID - creating new conversation")
		if m.engine == nil {
			Log("handleSendMessage: no Nostop engine available")
			return m, nil
		}
		// Clear input and create conversation, then send message
		pendingContent := content
		m.input.Reset()
		return m, m.createConversationAndSend(pendingContent)
	}

	// Clear input first
	m.input.Reset()
	Log("handleSendMessage: input cleared, checking for archived topics")

	// Check if message might reference archived content
	if m.archiver != nil && m.convID != "" {
		Log("handleSendMessage: checking for archived topics")
		return m, m.checkForArchivedTopics(content)
	}

	// No archiver or conversation - send directly
	Log("handleSendMessage: sending directly")
	return m.sendMessageDirectly(content)
}

// createConversationAndSend creates a new conversation and sends the first message.
func (m *ChatModel) createConversationAndSend(content string) tea.Cmd {
	return func() tea.Msg {
		ctx := m.ctx
		Log("createConversationAndSend: creating conversation")
		conv, err := m.engine.NewConversation(ctx, "New Chat", "")
		if err != nil {
			Log("createConversationAndSend: error: %v", err)
			return StreamErrorMsg{Err: err}
		}
		Log("createConversationAndSend: created %q, will send message", conv.ID)
		// Return a special message that includes the conversation and pending content
		return conversationCreatedWithContentMsg{
			ConvID:  conv.ID,
			Title:   conv.Title,
			Content: content,
		}
	}
}

// checkForArchivedTopics checks if the message references archived topics.
func (m ChatModel) checkForArchivedTopics(message string) tea.Cmd {
	return func() tea.Msg {
		if m.archiver == nil {
			return RestoreCheckResultMsg{Message: message, Topics: nil}
		}

		ctx := m.ctx
		topics, err := m.archiver.FindTopicsToRestore(ctx, m.convID, message)
		if err != nil {
			// On error, just proceed without restore
			return RestoreCheckResultMsg{Message: message, Topics: nil}
		}

		// Convert to pointers
		topicPtrs := make([]*storage.Topic, len(topics))
		for i := range topics {
			topicPtrs[i] = &topics[i]
		}

		return RestoreCheckResultMsg{
			Message: message,
			Topics:  topicPtrs,
		}
	}
}

// sendMessageDirectly sends a message without restore check.
func (m *ChatModel) sendMessageDirectly(content string) (*ChatModel, tea.Cmd) {
	// Add user message to display
	m.messages = append(m.messages, ChatMessage{
		Role:    "user",
		Content: content,
		Topic:   m.topicName,
	})
	m.updateViewport()
	m.viewport.GotoBottom()

	// Create a fresh stream channel and start streaming.
	// Channel is created here (synchronous Update path) so that
	// waitForStream closures always capture the correct channel.
	m.streamCh = make(chan tea.Msg, 100)
	return m, startStream(m.streamCh, m.engine, m.ctx, m.convID, content)
}

// restoreAndSend restores a topic and then sends the message.
func (m ChatModel) restoreAndSend(topic *storage.Topic, message string) tea.Cmd {
	return func() tea.Msg {
		if topic == nil || m.archiver == nil {
			// Can't restore, just indicate we should send the message
			return RestoreCheckResultMsg{Message: message, Topics: nil}
		}

		ctx := m.ctx
		restored, err := m.archiver.RestoreTopic(ctx, topic.ID, 0.5, 0.6)
		if err != nil {
			return TopicRestoreCompleteMsg{Topic: nil, Error: err}
		}

		// First send the restore complete message, then we need to send the original message
		// We return a batch that first notifies about restore, then sends the message
		return RestoreAndSendMsg{
			RestoredTopic: restored,
			Message:       message,
		}
	}
}

// RestoreCheckResultMsg is sent after checking for archived topics.
type RestoreCheckResultMsg struct {
	Message string
	Topics  []*storage.Topic
}

// RestoreAndSendMsg is sent when a topic was restored and message should be sent.
type RestoreAndSendMsg struct {
	RestoredTopic *storage.Topic
	Message       string
}

// startStream creates a command that streams the response from the Nostop engine.
// The channel and context are captured at call time, so rapid re-invocations
// write to separate channels — no cross-talk between old and new streams.
func startStream(ch chan tea.Msg, engine *nostop.Nostop, ctx context.Context, convID, message string) tea.Cmd {
	return func() tea.Msg {
		Log("startStream: starting stream for convID=%q message=%q", convID, message)

		go func() {
			defer close(ch)

			var finalContent strings.Builder

			// Create callback for streaming
			callback := func(event *api.StreamEvent) error {
				switch event.Type {
				case api.StreamEventContentBlockDelta:
					if event.Delta != nil && event.Delta.Type == "text_delta" {
						finalContent.WriteString(event.Delta.Text)
						ch <- StreamChunkMsg{Text: event.Delta.Text}
					}
				}
				return nil
			}

			// Create tool callback for agentic loop events
			toolCallback := func(event nostop.ToolEvent) {
				switch event.Type {
				case nostop.ToolEventStart:
					// Reset the content builder — intermediate text from
					// prior iterations is not the final response.
					finalContent.Reset()
					ch <- ToolCallMsg{
						Name:  event.Name,
						ID:    event.ID,
						Input: event.Input,
					}
				case nostop.ToolEventDone, nostop.ToolEventError:
					ch <- ToolResultMsg{
						Name:    event.Name,
						Output:  event.Output,
						IsError: event.IsError,
					}
				}
			}

			// Call Nostop.SendStream
			Log("startStream: calling Nostop.SendStream")
			err := engine.SendStream(ctx, convID, message, callback, toolCallback)
			if err != nil {
				Log("startStream: error from SendStream: %v", err)
				ch <- StreamErrorMsg{Err: err}
				return
			}

			// Create response object
			Log("startStream: stream complete, content length=%d", finalContent.Len())
			resp := &nostop.Response{
				Content: finalContent.String(),
			}

			ch <- StreamDoneMsg{Response: resp}
		}()

		// Return StreamStartMsg immediately, then use waitForStream to get subsequent messages
		return StreamStartMsg{}
	}
}

// waitForStream returns a command that reads the next message from the stream channel.
// The channel reference is captured at call time, so each invocation reads from
// the channel that was live when the command was created.
func (m ChatModel) waitForStream() tea.Cmd {
	ch := m.streamCh
	return func() tea.Msg {
		if ch == nil {
			Log("waitForStream: streamCh is nil")
			return StreamErrorMsg{Err: fmt.Errorf("stream channel not initialized")}
		}
		msg, ok := <-ch
		if !ok {
			Log("waitForStream: channel closed unexpectedly")
			return StreamErrorMsg{Err: fmt.Errorf("stream channel closed")}
		}
		Log("waitForStream: received %T", msg)
		return msg
	}
}

// updateViewport updates the viewport content with rendered messages.
func (m *ChatModel) updateViewport() {
	content := m.renderMessages()
	m.viewport.SetContent(content)
}

// renderMessages renders all messages for display in the viewport.
func (m ChatModel) renderMessages() string {
	if len(m.messages) == 0 {
		return m.styles.Placeholder.Render("Start a conversation by typing a message...")
	}

	var b strings.Builder
	maxWidth := m.width - 12

	for i, msg := range m.messages {
		if i > 0 {
			b.WriteString("\n\n")
		}

		switch msg.Role {
		case "user":
			label := m.styles.UserLabel.Render("You:")
			b.WriteString(label)
			b.WriteString("\n")
			content := m.wrapText(msg.Content, maxWidth)
			b.WriteString(m.styles.UserMessage.Render(content))

		case "assistant":
			label := m.styles.AssistantLabel.Render("Assistant:")
			b.WriteString(label)
			b.WriteString("\n")
			content := m.wrapText(msg.Content, maxWidth)
			if content == "" && m.streaming {
				content = m.styles.Placeholder.Render("...")
			}
			b.WriteString(m.styles.AssistantMessage.Render(content))

		case "system":
			label := m.styles.SystemLabel.Render("System:")
			b.WriteString(label)
			b.WriteString("\n")
			content := m.wrapText(msg.Content, maxWidth)
			b.WriteString(m.styles.SystemMessage.Render(content))

		case "tool":
			toolDisplay := msg.ToolName
			if msg.ToolTarget != "" {
				toolDisplay += " " + msg.ToolTarget
			}
			label := m.styles.ToolLabel.Render("Tool: " + toolDisplay)
			b.WriteString(label)
			b.WriteString("\n")
			content := m.wrapText(msg.Content, maxWidth)
			if msg.IsError {
				b.WriteString(m.styles.ToolError.Render(content))
			} else {
				b.WriteString(m.styles.ToolOutput.Render(content))
			}
		}
	}

	return b.String()
}

// wrapText wraps text to fit within maxWidth.
func (m ChatModel) wrapText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return text
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}

		// Simple word wrapping
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}

		currentLine := words[0]
		for _, word := range words[1:] {
			if lipgloss.Width(currentLine+" "+word) <= maxWidth {
				currentLine += " " + word
			} else {
				result.WriteString(currentLine)
				result.WriteString("\n")
				currentLine = word
			}
		}
		result.WriteString(currentLine)
	}

	return result.String()
}

// toolTarget extracts the primary file/directory target from tool input
// for display purposes. Returns "" if no recognizable target is found.
func toolTarget(input map[string]any) string {
	// Priority order: most specific parameter names first.
	keys := []string{"file_path", "file", "dir", "path", "source", "command", "exec", "query", "input"}
	for _, key := range keys {
		v, ok := input[key]
		if !ok {
			continue
		}
		switch val := v.(type) {
		case string:
			if val == "" {
				continue
			}
			// For commands/queries, truncate to keep display compact
			if key == "command" || key == "input" || key == "query" || key == "exec" {
				if len(val) > 40 {
					return val[:37] + "..."
				}
			}
			return val
		case []any:
			// Array of paths (checkfor dirs, repfor dirs, conflicts files)
			if len(val) == 0 {
				continue
			}
			if s, ok := val[0].(string); ok {
				if len(val) == 1 {
					return s
				}
				return fmt.Sprintf("%s (+%d more)", s, len(val)-1)
			}
		}
	}
	return ""
}

// formatToolOutput extracts readable content from JSON tool output.
// Falls back to truncated raw output if parsing fails.
func formatToolOutput(output string, maxLen int) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(output), &obj); err != nil {
		return truncateToolOutput(output, maxLen)
	}

	// read: {"file", "content", "total_lines", "from_line", "to_line"}
	if content, ok := obj["content"].(string); ok {
		var header string
		if file, ok := obj["file"].(string); ok {
			header = filepath.Base(file) + ":\n"
		}
		return truncateToolOutput(header+content, maxLen)
	}

	// bash: {"stdout", "stderr", "exit_code"}
	if _, hasStdout := obj["stdout"]; hasStdout {
		var b strings.Builder
		exitCode := 0
		if ec, ok := obj["exit_code"].(float64); ok {
			exitCode = int(ec)
		}
		if stderr, ok := obj["stderr"].(string); ok && stderr != "" {
			b.WriteString(stderr)
			b.WriteString("\n")
		}
		if stdout, ok := obj["stdout"].(string); ok && stdout != "" {
			b.WriteString(stdout)
		}
		if exitCode != 0 {
			fmt.Fprintf(&b, "\n(exit %d)", exitCode)
		}
		result := strings.TrimSpace(b.String())
		if result == "" {
			return "(no output)"
		}
		return truncateToolOutput(result, maxLen)
	}

	// write: {"file", "bytes_written", "status": "ok"}
	if status, ok := obj["status"].(string); ok && status == "ok" {
		if file, ok := obj["file"].(string); ok {
			bytes := 0
			if bw, ok := obj["bytes_written"].(float64); ok {
				bytes = int(bw)
			}
			return fmt.Sprintf("wrote %d bytes to %s", bytes, filepath.Base(file))
		}
	}

	// stump: {"root", "stats": {"dirs", "files", "filtered"}, "tree"}
	if stats, ok := obj["stats"].(map[string]any); ok {
		root, _ := obj["root"].(string)
		dirs, _ := stats["dirs"].(float64)
		files, _ := stats["files"].(float64)
		if root == "" {
			root = "."
		}
		return fmt.Sprintf("%s: %d dirs, %d files", root, int(dirs), int(files))
	}

	// cleanDiff: {"summary": {"files_changed", "insertions", "deletions"}, "files"}
	if summaryObj, ok := obj["summary"].(map[string]any); ok {
		if _, hasFChanged := summaryObj["files_changed"]; hasFChanged {
			fc, _ := summaryObj["files_changed"].(float64)
			ins, _ := summaryObj["insertions"].(float64)
			del, _ := summaryObj["deletions"].(float64)
			return fmt.Sprintf("%d files changed, +%d -%d", int(fc), int(ins), int(del))
		}
		// imports: {"summary": {"total_files", "total_imports"}, "files", "packages"}
		if tf, ok := summaryObj["total_files"].(float64); ok {
			ti, _ := summaryObj["total_imports"].(float64)
			return fmt.Sprintf("%d imports across %d files", int(ti), int(tf))
		}
	}

	// checkfor: {"matches": [...]}
	if matches, ok := obj["matches"].([]any); ok {
		return fmt.Sprintf("%d matches", len(matches))
	}

	// repfor: {"files_modified", "replacements"}
	if n, ok := obj["files_modified"].(float64); ok {
		replacements, _ := obj["replacements"].(float64)
		return fmt.Sprintf("%d replacements in %d files", int(replacements), int(n))
	}

	// sig: {"file", "functions", "types", "constants", "variables"}
	if _, hasFunctions := obj["functions"]; hasFunctions {
		file, _ := obj["file"].(string)
		nFuncs, nTypes := 0, 0
		if fns, ok := obj["functions"].([]any); ok {
			nFuncs = len(fns)
		}
		if tps, ok := obj["types"].([]any); ok {
			nTypes = len(tps)
		}
		return fmt.Sprintf("%s: %d functions, %d types", filepath.Base(file), nFuncs, nTypes)
	}

	// errs: {"errors", "format", "count", "files", "summary"}
	if count, ok := obj["count"].(float64); ok {
		if nFiles, ok := obj["files"].(float64); ok {
			format, _ := obj["format"].(string)
			if format != "" {
				return fmt.Sprintf("%d errors in %d files (%s)", int(count), int(nFiles), format)
			}
			return fmt.Sprintf("%d errors in %d files", int(count), int(nFiles))
		}
	}

	// notab: {"file", "replacements", "lines_affected", "direction"}
	if dir, ok := obj["direction"].(string); ok {
		file, _ := obj["file"].(string)
		replacements, _ := obj["replacements"].(float64)
		lines, _ := obj["lines_affected"].(float64)
		return fmt.Sprintf("%s: %d replacements on %d lines (%s)", filepath.Base(file), int(replacements), int(lines), dir)
	}

	// tabcount: {"file", "total_lines", "lines": [...]}
	if lines, ok := obj["lines"].([]any); ok {
		if _, hasTotal := obj["total_lines"]; hasTotal {
			file, _ := obj["file"].(string)
			total, _ := obj["total_lines"].(float64)
			return fmt.Sprintf("%s: %d/%d lines with tabs", filepath.Base(file), len(lines), int(total))
		}
	}

	// delete: {"original_path", "trash_path", "type", "size"}
	if trashPath, ok := obj["trash_path"].(string); ok {
		origPath, _ := obj["original_path"].(string)
		_ = trashPath
		return fmt.Sprintf("moved %s to Trash", filepath.Base(origPath))
	}

	// utf8: {"file", "detected", "issues", "bytes_in", "bytes_out", "status"}
	if detected, ok := obj["detected"].(string); ok {
		if _, hasBytes := obj["bytes_in"]; hasBytes {
			file, _ := obj["file"].(string)
			status, _ := obj["status"].(string)
			return fmt.Sprintf("%s: %s (%s)", filepath.Base(file), detected, status)
		}
	}

	// conflicts: {"files", "total", "has_diff3", "summary"}
	if total, ok := obj["total"].(float64); ok {
		if _, hasHasDiff3 := obj["has_diff3"]; hasHasDiff3 {
			return fmt.Sprintf("%d conflicts", int(total))
		}
	}

	// Generic: any tool with a "summary" string (split, splice, and future tools)
	if summary, ok := obj["summary"].(string); ok && summary != "" {
		return truncateToolOutput(summary, maxLen)
	}

	// Generic: any tool with a "status" string
	if status, ok := obj["status"].(string); ok {
		if file, ok := obj["file"].(string); ok {
			return fmt.Sprintf("%s: %s", filepath.Base(file), status)
		}
		return status
	}

	// Fallback: truncated raw output
	return truncateToolOutput(output, maxLen)
}

// truncateToolOutput truncates tool output for display in the TUI.
func truncateToolOutput(output string, maxLen int) string {
	if len(output) <= maxLen {
		return output
	}
	return output[:maxLen] + "\n... (truncated)"
}

// SetSize updates the chat model dimensions.
func (m *ChatModel) SetSize(width, height int) {
	m.width = width
	m.height = height

	// Update textarea — match content area inside Input style decoration
	m.input.SetWidth(width - 10)

	// Update viewport
	inputHeight := 3
	vpHeight := height - inputHeight - 4
	if vpHeight < 5 {
		vpHeight = 5
	}
	m.viewport.Width = width - 2
	m.viewport.Height = vpHeight

	// Re-render messages for new width
	m.updateViewport()
}

// SetConversation sets the active conversation.
func (m *ChatModel) SetConversation(convID string) {
	m.convID = convID
}

// GetConversation returns the active conversation ID.
func (m ChatModel) GetConversation() string {
	return m.convID
}

// IsStreaming returns true if currently streaming a response.
func (m ChatModel) IsStreaming() bool {
	return m.streaming
}

// SetTopic sets the current topic name.
func (m *ChatModel) SetTopic(name string) {
	m.topicName = name
}

// GetTopic returns the current topic name.
func (m ChatModel) GetTopic() string {
	return m.topicName
}

// LoadMessages loads existing messages from storage.
func (m *ChatModel) LoadMessages(ctx context.Context) error {
	if m.convID == "" || m.engine == nil {
		return nil
	}

	messages, err := m.engine.GetActiveMessages(ctx, m.convID)
	if err != nil {
		return err
	}

	m.messages = make([]ChatMessage, 0, len(messages))
	for _, msg := range messages {
		m.messages = append(m.messages, ChatMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	m.updateViewport()
	m.viewport.GotoBottom()

	return nil
}

// ClearMessages clears all displayed messages.
func (m *ChatModel) ClearMessages() {
	m.messages = nil
	m.updateViewport()
}

// Focus sets focus to the input textarea.
func (m *ChatModel) Focus() tea.Cmd {
	return m.input.Focus()
}

// Blur removes focus from the input textarea.
func (m *ChatModel) Blur() {
	m.input.Blur()
}

// StreamingCmd returns a command that continues processing stream events.
// This is used to process remaining events after the initial one.
func StreamingCmd(eventChan <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-eventChan
		if !ok {
			return io.EOF
		}
		return msg
	}
}
