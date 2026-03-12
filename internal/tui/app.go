package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/user/rlm/internal/topic"
	"github.com/user/rlm/pkg/rlm"
)

// View represents the current active view in the application.
type View int

const (
	// ViewChat is the main chat/conversation view.
	ViewChat View = iota
	// ViewHistory is the conversation history browser.
	ViewHistory
	// ViewTopics is the topics overview.
	ViewTopics
	// ViewDebug is the debug/context info view.
	ViewDebug
)

// String returns the display name for a view.
func (v View) String() string {
	switch v {
	case ViewChat:
		return "Chat"
	case ViewHistory:
		return "History"
	case ViewTopics:
		return "Topics"
	case ViewDebug:
		return "Debug"
	default:
		return "Unknown"
	}
}

// Key bindings documentation:
//
// Global keys (available in all views):
//   - ctrl+n: New conversation
//   - ctrl+h: Switch to History view
//   - ctrl+t: Switch to Topics view
//   - ctrl+d: Switch to Debug view
//   - ctrl+c / esc: Quit application
//   - tab: Cycle through views (Chat -> History -> Topics -> Debug -> Chat)
//   - shift+tab: Cycle views in reverse
//
// Chat view specific:
//   - enter: Send message
//   - up/down: Scroll through messages
//
// History view specific:
//   - up/down: Navigate conversation list
//   - enter: Select conversation
//   - delete: Delete conversation
//
// Topics view specific:
//   - up/down: Navigate topic list
//   - enter: View topic details
//   - r: Restore archived topic
//
// Debug view specific:
//   - r: Refresh context info

// App is the main Bubbletea model for the RLM TUI application.
type App struct {
	// rlm is the RLM engine instance.
	rlm *rlm.RLM

	// view is the currently active view.
	view View

	// width is the terminal width.
	width int

	// height is the terminal height.
	height int

	// convID is the current conversation ID.
	convID string

	// err holds any current error state.
	err error

	// styles contains all lipgloss styles.
	styles Styles

	// ready indicates if the app has received initial window size.
	ready bool

	// quitting indicates the app is shutting down.
	quitting bool

	// chatModel is the chat view model.
	chatModel *ChatModel

	// debugModel is the debug/context info view model.
	debugModel *DebugModel

	// contextMgr is the context manager for token tracking.
	contextMgr *rlm.ContextManager

	// tracker is the topic tracker.
	tracker *topic.TopicTracker

	// archiver is the topic archiver for restoration.
	archiver *rlm.Archiver

	// topicsModel is the topics overview view model.
	topicsModel *TopicsModel

	// history is the conversation history browser model.
	history *HistoryModel
}

// NewApp creates a new App instance with the given RLM engine.
func NewApp(engine *rlm.RLM) *App {
	return &App{
		rlm:    engine,
		view:   ViewChat,
		styles: DefaultStyles(),
	}
}

// Init implements tea.Model. It returns any initial commands.
func (a App) Init() tea.Cmd {
	Log("App.Init called - chatModel=%v, rlm=%v", a.chatModel != nil, a.rlm != nil)
	// chatModel is initialized on first WindowSizeMsg
	return nil
}

// Update implements tea.Model. It handles all messages and returns updated model and commands.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		Log("WindowSizeMsg: width=%d height=%d chatModel=%v", msg.Width, msg.Height, a.chatModel != nil)
		a.width = msg.Width
		a.height = msg.Height
		a.ready = true

		// Initialize or update models with new dimensions
		contentHeight := msg.Height - 6 // Account for header, status bar, help
		var cmds []tea.Cmd

		// Initialize or update chat model
		if a.chatModel == nil {
			Log("Initializing chatModel: rlm=%v width=%d contentHeight=%d", a.rlm != nil, msg.Width-4, contentHeight)
			cm := NewChatModel(a.rlm, msg.Width-4, contentHeight)
			a.chatModel = &cm
			Log("chatModel initialized: %v", a.chatModel != nil)
			cmds = append(cmds, a.chatModel.Init())
		} else {
			Log("Updating chatModel size: width=%d height=%d", msg.Width-4, contentHeight)
			a.chatModel.SetSize(msg.Width-4, contentHeight)
		}

		if a.history == nil && a.rlm != nil {
			a.history = NewHistoryModel(a.rlm.Storage(), msg.Width-4, contentHeight)
			cmds = append(cmds, a.history.Init())
		} else if a.history != nil {
			a.history.SetSize(msg.Width-4, contentHeight)
		}

		// Initialize or update topics model with new dimensions
		if a.topicsModel == nil && a.tracker != nil {
			a.topicsModel = NewTopicsModel(a.tracker, a.archiver, a.convID, msg.Width-4, contentHeight)
			cmds = append(cmds, a.topicsModel.Init())
		} else if a.topicsModel != nil {
			a.topicsModel.SetSize(msg.Width-4, contentHeight)
		}

		// Initialize or update debug model with new dimensions
		if a.debugModel == nil {
			a.debugModel = NewDebugModel(a.contextMgr, a.tracker, a.convID, msg.Width-4, contentHeight)
		} else {
			a.debugModel.SetSize(msg.Width-4, contentHeight)
		}

		if len(cmds) > 0 {
			return a, tea.Batch(cmds...)
		}
		return a, nil

	case tea.KeyMsg:
		Log("KeyMsg: key=%q view=%v chatModel=%v", msg.String(), a.view, a.chatModel != nil)

		// ctrl+c ALWAYS quits - never block emergency exit
		if msg.String() == "ctrl+c" {
			Log("ctrl+c pressed - quitting (emergency exit)")
			a.quitting = true
			return a, tea.Quit
		}

		// Don't intercept other keys during streaming
		if a.view == ViewChat && a.chatModel != nil && a.chatModel.IsStreaming() {
			Log("KeyMsg ignored - streaming in progress")
			return a, nil
		}

		// Handle global key bindings
		switch msg.String() {

		case "esc":
			// In chat view, esc blurs input; otherwise quit
			if a.view == ViewChat && a.chatModel != nil {
				a.chatModel.Blur()
				return a, nil
			}
			a.quitting = true
			return a, tea.Quit

		case "ctrl+n":
			// New conversation
			Log("ctrl+n pressed - creating new conversation")
			return a.handleNewConversation()

		case "ctrl+h":
			// Switch to history view and refresh
			a.view = ViewHistory
			if a.history != nil {
				return a, a.history.Refresh()
			}
			return a, nil

		case "ctrl+t":
			// Switch to topics view and refresh
			a.view = ViewTopics
			if a.topicsModel != nil {
				return a, a.topicsModel.Refresh()
			}
			return a, nil

		case "ctrl+d":
			// Switch to debug view
			a.view = ViewDebug
			return a, nil

		case "tab":
			// Cycle views forward
			a.view = (a.view + 1) % 4
			// Focus chat input when switching to chat view
			if a.view == ViewChat && a.chatModel != nil {
				return a, a.chatModel.Focus()
			}
			return a, nil

		case "shift+tab":
			// Cycle views backward
			a.view = (a.view + 3) % 4
			// Focus chat input when switching to chat view
			if a.view == ViewChat && a.chatModel != nil {
				return a, a.chatModel.Focus()
			}
			return a, nil
		}

		// Delegate to view-specific handlers
		return a.updateCurrentView(msg)

	case errMsg:
		a.err = msg.err
		return a, nil

	case clearErrMsg:
		a.err = nil
		return a, nil

	// Chat streaming messages
	case StreamStartMsg, StreamChunkMsg, StreamDoneMsg, StreamErrorMsg:
		// Pass streaming messages to chat model
		if a.chatModel == nil {
			return a, nil
		}
		var cmd tea.Cmd
		a.chatModel, cmd = a.chatModel.Update(msg)
		// Sync conversation ID from chat model
		a.convID = a.chatModel.GetConversation()
		return a, cmd

	case ConversationCreatedMsg:
		// Update both app and chat model
		a.convID = msg.ConvID
		if a.chatModel != nil {
			a.chatModel.SetConversation(msg.ConvID)
			var cmd tea.Cmd
			a.chatModel, cmd = a.chatModel.Update(msg)
			// Load existing messages if any
			return a, tea.Batch(cmd, a.loadChatMessages())
		}
		return a, a.loadChatMessages()

	// History view messages
	case ConversationSelectedMsg:
		// Switch to chat view with selected conversation
		a.convID = msg.ConvID
		a.view = ViewChat
		if a.chatModel != nil {
			a.chatModel.SetConversation(msg.ConvID)
			// Load messages for the selected conversation
			return a, tea.Batch(a.chatModel.Focus(), a.loadChatMessages())
		}
		return a, a.loadChatMessages()

	case ConversationsLoadedMsg, ConversationsLoadErrorMsg, ConversationDeletedMsg, ConversationDeleteErrorMsg:
		// Delegate to history model
		if a.history != nil {
			a.history, _ = a.history.Update(msg)
		}
		return a, nil

	// Topics view messages
	case TopicsLoadedMsg:
		// Delegate to topics model
		if a.topicsModel != nil {
			var cmd tea.Cmd
			*a.topicsModel, cmd = a.topicsModel.Update(msg)
			return a, cmd
		}
		return a, nil

	case TopicRestoredMsg:
		// Delegate to topics model and log to debug
		var cmds []tea.Cmd
		if a.topicsModel != nil {
			var cmd tea.Cmd
			*a.topicsModel, cmd = a.topicsModel.Update(msg)
			cmds = append(cmds, cmd)
		}
		// Add archive event for restored topic
		if msg.Topic != nil {
			a.AddArchiveEvent(msg.Topic.Name, msg.Topic.TokenCount, true)
		}
		return a, tea.Batch(cmds...)

	// Chat restore messages (pass to chat model)
	case RestoreCheckResultMsg, RestoreAndSendMsg, TopicRestoreCompleteMsg:
		var cmd tea.Cmd
		a.chatModel, cmd = a.chatModel.Update(msg)
		// If a topic was restored via chat, log to debug
		if restoreMsg, ok := msg.(TopicRestoreCompleteMsg); ok && restoreMsg.Topic != nil {
			a.AddArchiveEvent(restoreMsg.Topic.Name, restoreMsg.Topic.TokenCount, true)
		}
		return a, cmd

	// Debug view messages
	case ContextUsageMsg, RefreshDebugMsg, TickMsg, DebugErrorMsg:
		// Delegate to debug model
		if a.debugModel != nil {
			var cmd tea.Cmd
			*a.debugModel, cmd = a.debugModel.Update(msg)
			return a, cmd
		}
		return a, nil
	}

	// Pass other messages to current view
	if a.view == ViewChat {
		var cmd tea.Cmd
		a.chatModel, cmd = a.chatModel.Update(msg)
		return a, cmd
	}

	return a, nil
}

// View implements tea.Model. It renders the current view.
func (a App) View() string {
	Log("App.View called: ready=%v quitting=%v view=%v chatModel=%v", a.ready, a.quitting, a.view, a.chatModel != nil)
	if a.quitting {
		return "Goodbye!\n"
	}

	if !a.ready {
		Log("App.View: not ready yet")
		return "Initializing...\n"
	}

	var b strings.Builder

	// Render header with tabs
	b.WriteString(a.renderHeader())
	b.WriteString("\n")

	// Render error if present
	if a.err != nil {
		b.WriteString(a.renderError())
		b.WriteString("\n")
	}

	// Render current view content
	b.WriteString(a.renderCurrentView())

	// Render status bar
	b.WriteString("\n")
	b.WriteString(a.renderStatusBar())

	// Render help
	b.WriteString("\n")
	b.WriteString(a.renderHelp())

	return a.styles.App.Render(b.String())
}

// renderHeader renders the application header with view tabs.
func (a App) renderHeader() string {
	title := a.styles.Title.Render("RLM")
	subtitle := a.styles.StatusText.Render(" - Recursive Language Model")

	// Render view tabs
	tabs := make([]string, 4)
	views := []View{ViewChat, ViewHistory, ViewTopics, ViewDebug}
	for i, v := range views {
		if v == a.view {
			tabs[i] = a.styles.TabActive.Render(v.String())
		} else {
			tabs[i] = a.styles.TabInactive.Render(v.String())
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	header := lipgloss.JoinHorizontal(
		lipgloss.Top,
		title,
		subtitle,
		strings.Repeat(" ", max(0, a.width-lipgloss.Width(title)-lipgloss.Width(subtitle)-lipgloss.Width(tabBar)-4)),
		tabBar,
	)

	return a.styles.Header.Width(a.width - 2).Render(header)
}

// renderError renders the current error message.
func (a App) renderError() string {
	if a.err == nil {
		return ""
	}
	label := a.styles.ErrorLabel.Render("ERROR")
	msg := a.styles.Error.Render(a.err.Error())
	return label + " " + msg
}

// renderCurrentView renders the content for the current view.
func (a App) renderCurrentView() string {
	// Calculate available height for content
	// Header: ~3 lines, status bar: 1 line, help: 1 line, error: 1 line if present
	contentHeight := a.height - 6
	if a.err != nil {
		contentHeight--
	}
	if contentHeight < 1 {
		contentHeight = 1
	}

	switch a.view {
	case ViewChat:
		// Use the chat model view, but update its size first
		if a.chatModel != nil {
			a.chatModel.SetSize(a.width-4, contentHeight)
			return a.chatModel.View()
		}
		return "Initializing..."
	case ViewHistory:
		if a.history != nil {
			return a.history.View()
		}
		return a.renderHistoryPlaceholder(contentHeight)
	case ViewTopics:
		if a.topicsModel != nil {
			return a.topicsModel.View()
		}
		return a.renderTopicsPlaceholder(contentHeight)
	case ViewDebug:
		if a.debugModel != nil {
			return a.debugModel.View()
		}
		return a.renderDebugPlaceholder(contentHeight)
	default:
		return ""
	}
}

// renderChatPlaceholder renders placeholder content for the chat view.
func (a App) renderChatPlaceholder(height int) string {
	var b strings.Builder

	// Show current conversation info
	if a.convID != "" {
		b.WriteString(a.styles.Info.Render(fmt.Sprintf("Conversation: %s", a.convID)))
		b.WriteString("\n\n")
	} else {
		b.WriteString(a.styles.SystemMessage.Render("No conversation selected. Press Ctrl+N to start a new conversation."))
		b.WriteString("\n\n")
	}

	// Placeholder for messages area
	panel := a.styles.Panel.
		Width(a.width - 6).
		Height(height - 4).
		Render("Chat messages will appear here.\n\nThis view will show:\n- User messages (cyan)\n- Assistant responses (pink)\n- System notifications (orange)\n- Streaming responses")

	b.WriteString(panel)

	// Placeholder for input area
	b.WriteString("\n\n")
	inputPrompt := a.styles.InputPrompt.Render("> ")
	inputPlaceholder := a.styles.Placeholder.Render("Type your message here...")
	b.WriteString(inputPrompt + inputPlaceholder)

	return b.String()
}

// renderHistoryPlaceholder renders placeholder content for the history view.
func (a App) renderHistoryPlaceholder(height int) string {
	var b strings.Builder

	b.WriteString(a.styles.PanelTitle.Render("Conversation History"))
	b.WriteString("\n\n")

	panel := a.styles.Panel.
		Width(a.width - 6).
		Height(height - 4).
		Render("Conversation history browser will appear here.\n\nThis view will show:\n- List of past conversations\n- Creation date and title\n- Message count and token usage\n- Search/filter functionality")

	b.WriteString(panel)

	return b.String()
}

// renderTopicsPlaceholder renders placeholder content for the topics view.
func (a App) renderTopicsPlaceholder(height int) string {
	var b strings.Builder

	b.WriteString(a.styles.PanelTitle.Render("Topics Overview"))
	b.WriteString("\n\n")

	// Show topic legend
	legend := lipgloss.JoinHorizontal(
		lipgloss.Top,
		a.styles.TopicCurrent.Render("Current"),
		"  ",
		a.styles.TopicActive.Render("Active"),
		"  ",
		a.styles.TopicArchived.Render("Archived"),
	)
	b.WriteString(legend)
	b.WriteString("\n\n")

	panel := a.styles.Panel.
		Width(a.width - 6).
		Height(height - 6).
		Render("Topics overview will appear here.\n\nThis view will show:\n- Current topic (highlighted)\n- Active topics with relevance scores\n- Archived topics\n- Token usage per topic\n- Restore option for archived topics (press 'r')")

	b.WriteString(panel)

	return b.String()
}

// renderDebugPlaceholder renders placeholder content for the debug view.
func (a App) renderDebugPlaceholder(height int) string {
	var b strings.Builder

	b.WriteString(a.styles.PanelTitle.Render("Debug / Context Info"))
	b.WriteString("\n\n")

	// Show sample debug info
	debugInfo := []struct {
		label string
		value string
	}{
		{"Model", "claude-sonnet-4-5"},
		{"Max Context", "200,000 tokens"},
		{"Current Usage", "0 tokens (0%)"},
		{"Archive Threshold", "95%"},
		{"Archive Target", "50%"},
		{"Active Topics", "0"},
		{"Archived Topics", "0"},
	}

	for _, info := range debugInfo {
		label := a.styles.DebugLabel.Render(info.label + ":")
		value := a.styles.DebugValue.Render(info.value)
		b.WriteString(label + value + "\n")
	}

	b.WriteString("\n")

	// Show context usage bar placeholder
	b.WriteString(a.styles.DebugLabel.Render("Context Usage:"))
	b.WriteString("\n")
	b.WriteString(a.styles.RenderProgressBar(0.0, 40))
	b.WriteString(" 0%")

	b.WriteString("\n\n")

	panel := a.styles.Panel.
		Width(a.width - 6).
		Height(height - 16).
		Render("Additional debug information will appear here.\n\nThis view will show:\n- Real-time context usage\n- Topic allocation breakdown\n- Archive event history\n- API request/response stats")

	b.WriteString(panel)

	return b.String()
}

// renderStatusBar renders the status bar at the bottom.
func (a App) renderStatusBar() string {
	var status string
	if a.convID != "" {
		status = fmt.Sprintf("Conversation: %s", truncate(a.convID, 20))
	} else {
		status = "No active conversation"
	}

	viewName := a.view.String()
	padding := max(0, a.width-len(status)-len(viewName)-6)

	return a.styles.StatusBar.Width(a.width - 2).Render(
		status + strings.Repeat(" ", padding) + "[" + viewName + "]",
	)
}

// renderHelp renders the help text with key bindings.
func (a App) renderHelp() string {
	// Base global bindings
	globalBindings := []string{
		a.styles.RenderKeyBinding("ctrl+n", "new"),
		a.styles.RenderKeyBinding("ctrl+h", "history"),
		a.styles.RenderKeyBinding("ctrl+t", "topics"),
		a.styles.RenderKeyBinding("ctrl+d", "debug"),
		a.styles.RenderKeyBinding("tab", "switch view"),
		a.styles.RenderKeyBinding("esc", "quit"),
	}

	// Add view-specific help
	var viewHelp string
	switch a.view {
	case ViewHistory:
		if a.history != nil {
			viewHelp = a.history.RenderHelp()
		}
	case ViewTopics:
		if a.topicsModel != nil {
			// Topics view help: j/k (navigate), a (toggle archived), r (restore)
			viewHelp = a.styles.RenderKeyBinding("j/k", "navigate") + "  " +
				a.styles.RenderKeyBinding("a", "toggle archived")
			if a.topicsModel.ShowingArchived() && a.topicsModel.ArchivedCount() > 0 {
				viewHelp += "  " + a.styles.RenderKeyBinding("r", "restore")
			}
		}
	case ViewDebug:
		// Debug view help: r (toggle auto-refresh), R (manual refresh)
		viewHelp = a.styles.RenderKeyBinding("r", "toggle auto-refresh") + "  " +
			a.styles.RenderKeyBinding("R", "refresh now")
	}

	if viewHelp != "" {
		return a.styles.Help.Render(viewHelp + "  |  " + strings.Join(globalBindings, "  "))
	}
	return a.styles.Help.Render(strings.Join(globalBindings, "  "))
}

// updateCurrentView delegates key handling to the current view.
func (a App) updateCurrentView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch a.view {
	case ViewChat:
		// Delegate to chat model
		if a.chatModel != nil {
			var cmd tea.Cmd
			a.chatModel, cmd = a.chatModel.Update(msg)
			return a, cmd
		}
		return a, nil
	case ViewHistory:
		// Delegate to history model
		if a.history != nil {
			var cmd tea.Cmd
			a.history, cmd = a.history.Update(msg)
			return a, cmd
		}
		return a, nil
	case ViewTopics:
		// Delegate to topics model
		if a.topicsModel != nil {
			var cmd tea.Cmd
			*a.topicsModel, cmd = a.topicsModel.Update(msg)
			return a, cmd
		}
		return a, nil
	case ViewDebug:
		// Delegate to debug model
		if a.debugModel != nil {
			var cmd tea.Cmd
			*a.debugModel, cmd = a.debugModel.Update(msg)
			return a, cmd
		}
		return a, nil
	}
	return a, nil
}

// handleNewConversation creates a new conversation.
func (a App) handleNewConversation() (tea.Model, tea.Cmd) {
	Log("handleNewConversation: rlm=%v current convID=%q", a.rlm != nil, a.convID)

	if a.rlm == nil {
		Log("handleNewConversation: no RLM engine")
		return a, nil
	}

	a.view = ViewChat

	// Create new conversation asynchronously
	return a, func() tea.Msg {
		ctx := context.Background()
		conv, err := a.rlm.NewConversation(ctx, "New Chat", "")
		if err != nil {
			Log("handleNewConversation: error creating conversation: %v", err)
			return errMsg{err: err}
		}
		Log("handleNewConversation: created conversation %q", conv.ID)
		return ConversationCreatedMsg{
			ConvID: conv.ID,
			Title:  conv.Title,
		}
	}
}

// SetConversation sets the current conversation ID.
func (a *App) SetConversation(convID string) {
	a.convID = convID
}

// GetConversation returns the current conversation ID.
func (a *App) GetConversation() string {
	return a.convID
}

// SetError sets the current error state.
func (a *App) SetError(err error) {
	a.err = err
}

// ClearError clears the current error state.
func (a *App) ClearError() {
	a.err = nil
}

// RLM returns the underlying RLM engine.
func (a *App) RLM() *rlm.RLM {
	return a.rlm
}

// SetTracker sets the topic tracker.
func (a *App) SetTracker(tracker *topic.TopicTracker) {
	a.tracker = tracker
}

// SetArchiver sets the topic archiver.
func (a *App) SetArchiver(archiver *rlm.Archiver) {
	a.archiver = archiver
	// Also set on chat model for restore detection
	if a.chatModel != nil {
		a.chatModel.SetArchiver(archiver)
	}
}

// Tracker returns the topic tracker.
func (a *App) Tracker() *topic.TopicTracker {
	return a.tracker
}

// Archiver returns the topic archiver.
func (a *App) Archiver() *rlm.Archiver {
	return a.archiver
}

// SetContextManager sets the context manager.
func (a *App) SetContextManager(mgr *rlm.ContextManager) {
	a.contextMgr = mgr
	if a.debugModel != nil {
		a.debugModel = NewDebugModel(mgr, a.tracker, a.convID, a.width-4, a.height-6)
	}
}

// ContextManager returns the context manager.
func (a *App) ContextManager() *rlm.ContextManager {
	return a.contextMgr
}

// DebugModel returns the debug model.
func (a *App) DebugModel() *DebugModel {
	return a.debugModel
}

// AddArchiveEvent adds an archive event to the debug model's history.
func (a *App) AddArchiveEvent(topicName string, tokens int, isRestore bool) {
	if a.debugModel != nil {
		a.debugModel.AddArchiveEvent(topicName, tokens, isRestore)
	}
}

// --- Message types ---

// errMsg is a message indicating an error occurred.
type errMsg struct {
	err error
}

// clearErrMsg is a message to clear the current error.
type clearErrMsg struct{}

// --- Helper functions ---

// truncate truncates a string to the specified length with ellipsis.
func truncate(s string, length int) string {
	if len(s) <= length {
		return s
	}
	if length <= 3 {
		return s[:length]
	}
	return s[:length-3] + "..."
}

// loadChatMessages loads messages for the current conversation into the chat model.
func (a *App) loadChatMessages() tea.Cmd {
	return func() tea.Msg {
		if a.convID == "" || a.rlm == nil || a.chatModel == nil {
			return nil
		}
		ctx := context.Background()
		if err := a.chatModel.LoadMessages(ctx); err != nil {
			return errMsg{err: err}
		}
		return nil
	}
}
