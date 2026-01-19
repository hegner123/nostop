package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	// Return nil for now - sub-views will add their own init commands
	return nil
}

// Update implements tea.Model. It handles all messages and returns updated model and commands.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.ready = true
		return a, nil

	case tea.KeyMsg:
		// Handle global key bindings
		switch msg.String() {
		case "ctrl+c", "esc":
			a.quitting = true
			return a, tea.Quit

		case "ctrl+n":
			// New conversation
			return a.handleNewConversation()

		case "ctrl+h":
			// Switch to history view
			a.view = ViewHistory
			return a, nil

		case "ctrl+t":
			// Switch to topics view
			a.view = ViewTopics
			return a, nil

		case "ctrl+d":
			// Switch to debug view
			a.view = ViewDebug
			return a, nil

		case "tab":
			// Cycle views forward
			a.view = (a.view + 1) % 4
			return a, nil

		case "shift+tab":
			// Cycle views backward
			a.view = (a.view + 3) % 4
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
	}

	return a, nil
}

// View implements tea.Model. It renders the current view.
func (a App) View() string {
	if a.quitting {
		return "Goodbye!\n"
	}

	if !a.ready {
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
		return a.renderChatPlaceholder(contentHeight)
	case ViewHistory:
		return a.renderHistoryPlaceholder(contentHeight)
	case ViewTopics:
		return a.renderTopicsPlaceholder(contentHeight)
	case ViewDebug:
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
	bindings := []string{
		a.styles.RenderKeyBinding("ctrl+n", "new"),
		a.styles.RenderKeyBinding("ctrl+h", "history"),
		a.styles.RenderKeyBinding("ctrl+t", "topics"),
		a.styles.RenderKeyBinding("ctrl+d", "debug"),
		a.styles.RenderKeyBinding("tab", "switch view"),
		a.styles.RenderKeyBinding("esc", "quit"),
	}
	return a.styles.Help.Render(strings.Join(bindings, "  "))
}

// updateCurrentView delegates key handling to the current view.
func (a App) updateCurrentView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// For now, just return - actual view implementations will handle specific keys
	switch a.view {
	case ViewChat:
		// Chat view will handle: enter (send), up/down (scroll)
		return a, nil
	case ViewHistory:
		// History view will handle: up/down (navigate), enter (select), delete
		return a, nil
	case ViewTopics:
		// Topics view will handle: up/down (navigate), enter (details), r (restore)
		return a, nil
	case ViewDebug:
		// Debug view will handle: r (refresh)
		return a, nil
	}
	return a, nil
}

// handleNewConversation creates a new conversation.
func (a App) handleNewConversation() (tea.Model, tea.Cmd) {
	// This will be implemented to create a new conversation via the RLM engine
	// For now, just switch to chat view
	a.view = ViewChat
	a.convID = "" // Will be set when conversation is actually created
	return a, nil
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
