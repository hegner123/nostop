// Package tui provides the Bubbletea-based terminal user interface for RLM.
package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/user/rlm/internal/api"
	"github.com/user/rlm/internal/storage"
	"github.com/user/rlm/pkg/rlm"
)

// ChatMessage represents a message displayed in the chat view.
type ChatMessage struct {
	Role    string // "user" or "assistant"
	Content string
	Topic   string // topic name when message was sent
}

// ChatModel is the Bubbletea model for the chat view.
type ChatModel struct {
	rlm       *rlm.RLM
	archiver  *rlm.Archiver
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
	restorePrompt *RestorePrompt
	pendingMessage string // Message waiting for restore decision
	notifications  *NotificationManager
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
	Response *rlm.Response
}

// StreamErrorMsg indicates an error during streaming.
type StreamErrorMsg struct {
	Err error
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

// NewChatModel creates a new ChatModel with the given RLM engine and dimensions.
func NewChatModel(engine *rlm.RLM, width, height int) ChatModel {
	// Create textarea for input
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Enter to send, Shift+Enter for newline)"
	ta.Focus()
	ta.CharLimit = 0 // No character limit
	ta.ShowLineNumbers = false

	// Set textarea dimensions
	inputHeight := 3
	ta.SetWidth(width - 4)
	ta.SetHeight(inputHeight)

	// Create viewport for message history
	// Reserve space for input area and some padding
	vpHeight := height - inputHeight - 8
	if vpHeight < 5 {
		vpHeight = 5
	}
	vp := viewport.New(width-4, vpHeight)
	vp.SetContent("")

	return ChatModel{
		rlm:           engine,
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
func (m *ChatModel) SetArchiver(archiver *rlm.Archiver) {
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
		return m, waitForStreamMsg

	case StreamChunkMsg:
		if m.streaming && len(m.messages) > 0 {
			// Append to stream buffer
			m.streamBuf.WriteString(msg.Text)
			// Update last message content
			m.messages[len(m.messages)-1].Content = m.streamBuf.String()
			m.updateViewport()
			// Auto-scroll to bottom
			m.viewport.GotoBottom()
		}
		// Continue reading from stream channel
		return m, waitForStreamMsg

	case StreamDoneMsg:
		Log("StreamDoneMsg received - streaming complete")
		m.streaming = false
		if msg.Response != nil && len(m.messages) > 0 {
			// Update final message content
			m.messages[len(m.messages)-1].Content = msg.Response.Content
			// Check for topic shift
			if msg.Response.TopicShift != nil && msg.Response.TopicShift.Detected {
				m.topicName = msg.Response.TopicShift.NewTopicName
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
		userMsg := rlm.UserFriendlyMessage(msg.Err)
		action := rlm.SuggestAction(msg.Err)
		if action != rlm.ActionNone {
			userMsg = userMsg + " " + rlm.ActionMessage(action)
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

	// Show conversation status
	if m.convID == "" {
		b.WriteString(m.styles.SystemMessage.Render("Type a message to start a new conversation"))
		b.WriteString("\n\n")
	}

	// Show restore notifications
	if m.notifications != nil && m.notifications.HasNotifications() {
		b.WriteString(m.notifications.View())
		b.WriteString("\n")
	}

	// Render message viewport
	vpStyle := m.styles.Panel.Width(m.width - 6)
	b.WriteString(vpStyle.Render(m.viewport.View()))
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
	inputLabel := m.styles.InputPrompt.Render("> ")
	b.WriteString(inputLabel)
	b.WriteString("\n")
	inputStyle := m.styles.Input.Width(m.width - 6)
	b.WriteString(inputStyle.Render(m.input.View()))

	return b.String()
}

// handleSendMessage processes sending a message.
func (m *ChatModel) handleSendMessage() (*ChatModel, tea.Cmd) {
	content := strings.TrimSpace(m.input.Value())
	Log("handleSendMessage: content=%q convID=%q archiver=%v rlm=%v", content, m.convID, m.archiver != nil, m.rlm != nil)
	if content == "" {
		Log("handleSendMessage: empty content, ignoring")
		return m, nil
	}

	// Check if we have a conversation - create one if not
	if m.convID == "" {
		Log("handleSendMessage: no conversation ID - creating new conversation")
		if m.rlm == nil {
			Log("handleSendMessage: no RLM engine available")
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
		ctx := context.Background()
		Log("createConversationAndSend: creating conversation")
		conv, err := m.rlm.NewConversation(ctx, "New Chat", "")
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

		ctx := context.Background()
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

	// Start streaming response
	return m, m.streamResponse(content)
}

// restoreAndSend restores a topic and then sends the message.
func (m ChatModel) restoreAndSend(topic *storage.Topic, message string) tea.Cmd {
	return func() tea.Msg {
		if topic == nil || m.archiver == nil {
			// Can't restore, just indicate we should send the message
			return RestoreCheckResultMsg{Message: message, Topics: nil}
		}

		ctx := context.Background()
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

// streamChan is used to pass streaming events back to the model.
// This is stored globally to allow the waitForStreamMsg command to read from it.
var streamChan chan tea.Msg

// streamResponse creates a command that streams the response from the RLM engine.
func (m ChatModel) streamResponse(message string) tea.Cmd {
	return func() tea.Msg {
		Log("streamResponse: starting stream for convID=%q message=%q", m.convID, message)

		// Create a channel to receive stream events
		streamChan = make(chan tea.Msg, 100)

		go func() {
			defer close(streamChan)

			var finalContent strings.Builder

			// Create callback for streaming
			callback := func(event *api.StreamEvent) error {
				switch event.Type {
				case api.StreamEventContentBlockDelta:
					if event.Delta != nil && event.Delta.Type == "text_delta" {
						finalContent.WriteString(event.Delta.Text)
						streamChan <- StreamChunkMsg{Text: event.Delta.Text}
					}
				}
				return nil
			}

			// Call RLM.SendStream
			ctx := context.Background()
			Log("streamResponse: calling RLM.SendStream")
			err := m.rlm.SendStream(ctx, m.convID, message, callback)
			if err != nil {
				Log("streamResponse: error from SendStream: %v", err)
				streamChan <- StreamErrorMsg{Err: err}
				return
			}

			// Create response object
			Log("streamResponse: stream complete, content length=%d", finalContent.Len())
			resp := &rlm.Response{
				Content: finalContent.String(),
			}

			streamChan <- StreamDoneMsg{Response: resp}
		}()

		// Return StreamStartMsg immediately, then use waitForStreamMsg to get subsequent messages
		return StreamStartMsg{}
	}
}

// waitForStreamMsg waits for the next message from the stream channel.
func waitForStreamMsg() tea.Msg {
	if streamChan == nil {
		Log("waitForStreamMsg: streamChan is nil")
		return StreamErrorMsg{Err: fmt.Errorf("stream channel not initialized")}
	}
	msg, ok := <-streamChan
	if !ok {
		Log("waitForStreamMsg: channel closed unexpectedly")
		return StreamErrorMsg{Err: fmt.Errorf("stream channel closed")}
	}
	Log("waitForStreamMsg: received %T", msg)
	return msg
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

// SetSize updates the chat model dimensions.
func (m *ChatModel) SetSize(width, height int) {
	m.width = width
	m.height = height

	// Update textarea
	m.input.SetWidth(width - 4)

	// Update viewport
	inputHeight := 3
	vpHeight := height - inputHeight - 8
	if vpHeight < 5 {
		vpHeight = 5
	}
	m.viewport.Width = width - 4
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
	if m.convID == "" || m.rlm == nil {
		return nil
	}

	messages, err := m.rlm.GetActiveMessages(ctx, m.convID)
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
