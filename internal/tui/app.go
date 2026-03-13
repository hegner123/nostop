package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/hegner123/nostop/internal/topic"
	"github.com/hegner123/nostop/pkg/nostop"
)

// Key bindings:
//
// Global keys:
//   - ctrl+n: New conversation
//   - ctrl+h: Toggle history overlay
//   - ctrl+t: Toggle topics overlay
//   - ctrl+d: Toggle debug overlay
//   - ctrl+c: Quit application
//
// Chat (base view):
//   - enter: Send message
//   - pgup/pgdn: Scroll message history
//   - up/down: Scroll viewport (or move cursor in multi-line input)
//
// Overlays:
//   - esc: Close overlay (or cancel sub-dialog within overlay)

// App is the main Bubbletea model for the Nostop TUI application.
type App struct {
	engine     *nostop.Nostop
	ctx        context.Context
	cancel     context.CancelFunc
	width      int
	height     int
	convID     string
	err        error
	styles     Styles
	ready      bool
	quitting   bool
	chatModel  *ChatModel
	debugModel *DebugModel
	contextMgr *nostop.ContextManager
	tracker    *topic.TopicTracker
	archiver   *nostop.Archiver

	topicsModel *TopicsModel
	history     *HistoryModel

	// activeOverlay captures all key input and renders on top of the chat.
	activeOverlay ModalOverlay
}

// NewApp creates a new App instance with the given Nostop engine.
func NewApp(engine *nostop.Nostop, ctx context.Context) *App {
	appCtx, appCancel := context.WithCancel(ctx)
	return &App{
		engine: engine,
		ctx:    appCtx,
		cancel: appCancel,
		styles: DefaultStyles(),
	}
}

// Init implements tea.Model.
func (a App) Init() tea.Cmd {
	Log("App.Init called - chatModel=%v, engine=%v", a.chatModel != nil, a.engine != nil)
	return nil
}

// Update implements tea.Model.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		Log("WindowSizeMsg: width=%d height=%d chatModel=%v", msg.Width, msg.Height, a.chatModel != nil)
		a.width = msg.Width
		a.height = msg.Height
		a.ready = true

		contentHeight := msg.Height - 4 // header + help
		var cmds []tea.Cmd

		if a.chatModel == nil {
			cm := NewChatModel(a.engine, a.ctx, msg.Width-4, contentHeight)
			a.chatModel = &cm
			cmds = append(cmds, a.chatModel.Init())
		} else {
			a.chatModel.SetSize(msg.Width-4, contentHeight)
		}

		if a.history == nil && a.engine != nil {
			a.history = NewHistoryModel(a.engine.Storage(), a.ctx, msg.Width-4, contentHeight)
			cmds = append(cmds, a.history.Init())
		}

		if a.topicsModel == nil && a.tracker != nil {
			a.topicsModel = NewTopicsModel(a.tracker, a.archiver, a.convID, a.ctx, msg.Width-4, contentHeight)
			cmds = append(cmds, a.topicsModel.Init())
		}

		if a.debugModel == nil {
			a.debugModel = NewDebugModel(a.contextMgr, a.tracker, a.convID, a.ctx, msg.Width-4, contentHeight)
			cmds = append(cmds, a.debugModel.Init())
		}

		if len(cmds) > 0 {
			return a, tea.Batch(cmds...)
		}
		return a, nil

	case tea.KeyPressMsg:
		Log("KeyPressMsg: key=%q overlay=%v chatModel=%v", msg.String(), a.activeOverlay != nil, a.chatModel != nil)

		// ctrl+c always quits
		if msg.String() == "ctrl+c" {
			a.quitting = true
			a.cancel()
			return a, tea.Quit
		}

		// Don't intercept during streaming (except ctrl+c above)
		if a.chatModel != nil && a.chatModel.IsStreaming() {
			return a, nil
		}

		// Overlay toggle keys work regardless of overlay state
		switch msg.String() {
		case "ctrl+h":
			return a.toggleOverlay(a.history, func() tea.Cmd {
				if a.history != nil {
					return a.history.Refresh()
				}
				return nil
			})
		case "ctrl+t":
			return a.toggleOverlay(a.topicsModel, func() tea.Cmd {
				if a.topicsModel != nil {
					return a.topicsModel.Refresh()
				}
				return nil
			})
		case "ctrl+d":
			return a.toggleOverlay(a.debugModel, func() tea.Cmd {
				if a.debugModel != nil {
					return a.debugModel.refreshCmd()
				}
				return nil
			})
		}

		// If overlay is active, route all other keys to it
		if a.activeOverlay != nil {
			overlay, cmd := a.activeOverlay.OverlayUpdate(msg)
			if overlay == nil {
				// Overlay requested close
				a.activeOverlay = nil
				if a.chatModel != nil {
					return a, tea.Batch(cmd, a.chatModel.Focus())
				}
				return a, cmd
			}
			a.activeOverlay = overlay
			return a, cmd
		}

		// No overlay — handle chat-level keys
		switch msg.String() {
		case "ctrl+n":
			return a.handleNewConversation()
		case "esc":
			if a.chatModel != nil {
				a.chatModel.Blur()
			}
			return a, nil
		}

		// Delegate to chat model
		if a.chatModel != nil {
			var cmd tea.Cmd
			a.chatModel, cmd = a.chatModel.Update(msg)
			return a, cmd
		}
		return a, nil

	case errMsg:
		a.err = msg.err
		return a, nil

	case clearErrMsg:
		a.err = nil
		return a, nil

	// Chat streaming messages
	case StreamStartMsg, StreamChunkMsg, StreamDoneMsg, StreamErrorMsg:
		if a.chatModel == nil {
			return a, nil
		}
		var cmd tea.Cmd
		a.chatModel, cmd = a.chatModel.Update(msg)
		a.convID = a.chatModel.GetConversation()

		if _, ok := msg.(StreamDoneMsg); ok && a.convID != "" {
			var cmds []tea.Cmd
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			if a.debugModel != nil {
				a.debugModel.SetConversation(a.convID)
				cmds = append(cmds, a.debugModel.refreshCmd())
			}
			if a.topicsModel != nil {
				cmds = append(cmds, a.topicsModel.SetConversation(a.convID))
			}
			if a.chatModel.GetTopic() == "" && a.tracker != nil {
				if current := a.tracker.GetCurrentTopic(); current != nil {
					a.chatModel.SetTopic(current.Name)
					if a.chatModel.notifications != nil {
						a.chatModel.notifications.AddTopicNotification(current.Name, false)
					}
					a.chatModel.updateViewport()
				}
			}
			return a, tea.Batch(cmds...)
		}
		return a, cmd

	case ConversationCreatedMsg:
		a.convID = msg.ConvID
		if a.chatModel != nil {
			a.chatModel.SetConversation(msg.ConvID)
			var cmd tea.Cmd
			a.chatModel, cmd = a.chatModel.Update(msg)
			return a, tea.Batch(cmd, a.loadChatMessages())
		}
		return a, a.loadChatMessages()

	// History messages
	case ConversationSelectedMsg:
		a.activeOverlay = nil // Close history overlay
		a.convID = msg.ConvID
		if a.chatModel != nil {
			a.chatModel.SetConversation(msg.ConvID)
			return a, tea.Batch(a.chatModel.Focus(), a.loadChatMessages())
		}
		return a, a.loadChatMessages()

	case ConversationsLoadedMsg, ConversationsLoadErrorMsg, ConversationDeletedMsg, ConversationDeleteErrorMsg:
		if a.history != nil {
			a.history, _ = a.history.Update(msg)
		}
		return a, nil

	// Topics messages
	case TopicsLoadedMsg:
		if a.topicsModel != nil {
			var cmd tea.Cmd
			*a.topicsModel, cmd = a.topicsModel.Update(msg)
			return a, cmd
		}
		return a, nil

	case TopicRestoredMsg:
		var cmds []tea.Cmd
		if a.topicsModel != nil {
			var cmd tea.Cmd
			*a.topicsModel, cmd = a.topicsModel.Update(msg)
			cmds = append(cmds, cmd)
		}
		if msg.Topic != nil {
			a.AddArchiveEvent(msg.Topic.Name, msg.Topic.TokenCount, true)
		}
		return a, tea.Batch(cmds...)

	case TopicArchivedMsg:
		var cmds []tea.Cmd
		if a.topicsModel != nil {
			var cmd tea.Cmd
			*a.topicsModel, cmd = a.topicsModel.Update(msg)
			cmds = append(cmds, cmd)
		}
		if msg.Topic != nil {
			a.AddArchiveEvent(msg.Topic.Name, msg.Topic.TokenCount, false)
			if a.chatModel != nil && a.chatModel.notifications != nil {
				a.chatModel.notifications.AddArchiveNotification(msg.Topic.Name, msg.Topic.TokenCount)
				a.chatModel.updateViewport()
			}
		}
		return a, tea.Batch(cmds...)

	// Chat restore messages
	case RestoreCheckResultMsg, RestoreAndSendMsg, TopicRestoreCompleteMsg:
		var cmd tea.Cmd
		a.chatModel, cmd = a.chatModel.Update(msg)
		if restoreMsg, ok := msg.(TopicRestoreCompleteMsg); ok && restoreMsg.Topic != nil {
			a.AddArchiveEvent(restoreMsg.Topic.Name, restoreMsg.Topic.TokenCount, true)
		}
		return a, cmd

	// Debug messages
	case ContextUsageMsg, RefreshDebugMsg, TickMsg, DebugErrorMsg:
		if a.debugModel != nil {
			var cmd tea.Cmd
			*a.debugModel, cmd = a.debugModel.Update(msg)
			return a, cmd
		}
		return a, nil

	// Clipboard copy completed — clear selection highlight
	case copyDoneMsg:
		if a.chatModel != nil && a.chatModel.selection != nil {
			a.chatModel.selection.Clear()
		}
		return a, nil

	// Mouse events — route to chat model for text selection
	case tea.MouseClickMsg:
		if a.activeOverlay != nil || a.chatModel == nil {
			return a, nil
		}
		if msg.Button == tea.MouseLeft {
			col := msg.X - 1 // subtract App horizontal padding
			chatY := a.computeChatStartY()
			row := msg.Y - chatY - a.chatModel.VpRowOffset()
			if row >= 0 && row < a.chatModel.ViewportHeight() {
				return a, a.chatModel.HandleMouseClick(col, row)
			}
		}
		return a, nil

	case tea.MouseMotionMsg:
		if a.activeOverlay != nil || a.chatModel == nil {
			return a, nil
		}
		col := msg.X - 1
		chatY := a.computeChatStartY()
		row := msg.Y - chatY - a.chatModel.VpRowOffset()
		// Clamp row to viewport bounds — drag can exceed visible area
		if row < 0 {
			row = 0
		} else if row >= a.chatModel.ViewportHeight() {
			row = a.chatModel.ViewportHeight() - 1
		}
		a.chatModel.HandleMouseDrag(col, row)
		return a, nil

	case tea.MouseReleaseMsg:
		if a.activeOverlay != nil || a.chatModel == nil {
			return a, nil
		}
		return a, a.chatModel.HandleMouseUp()
	}

	// Pass other messages to chat model (mouse events, blink, etc.)
	if a.chatModel != nil {
		var cmd tea.Cmd
		a.chatModel, cmd = a.chatModel.Update(msg)
		return a, cmd
	}

	return a, nil
}

// View implements tea.Model.
func (a App) View() tea.View {
	if a.quitting {
		return tea.NewView("Goodbye!\n")
	}
	if !a.ready {
		return tea.NewView("Initializing...\n")
	}

	var b strings.Builder

	// Header
	b.WriteString(a.renderHeader())
	b.WriteString("\n")

	if a.err != nil {
		b.WriteString(a.renderError())
		b.WriteString("\n")
	}

	// Always render chat as the base view
	contentHeight := a.height - 4
	if a.err != nil {
		contentHeight--
	}
	if contentHeight < 1 {
		contentHeight = 1
	}
	if a.chatModel != nil {
		a.chatModel.SetSize(a.width-4, contentHeight)
		b.WriteString(a.chatModel.View())
	}

	b.WriteString("\n")
	b.WriteString(a.renderHelp())

	base := a.styles.App.Render(b.String())

	// Composite overlay on top if active
	var content string
	if a.activeOverlay != nil {
		content = RenderOverlay(base, a.activeOverlay, a.width, a.height)
	} else {
		content = base
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// toggleOverlay opens an overlay, or closes it if it's already the active one.
// Pressing a different overlay key switches to that overlay.
func (a App) toggleOverlay(target ModalOverlay, onOpen func() tea.Cmd) (tea.Model, tea.Cmd) {
	if target == nil {
		return a, nil
	}
	if a.activeOverlay == target {
		a.activeOverlay = nil
		if a.chatModel != nil {
			return a, a.chatModel.Focus()
		}
		return a, nil
	}
	a.activeOverlay = target
	if onOpen != nil {
		return a, onOpen()
	}
	return a, nil
}

// renderHeader renders the application header with optional topic name.
func (a App) renderHeader() string {
	title := a.styles.Title.Render("nostop")

	// Activity indicator when API is working
	if a.chatModel != nil && a.chatModel.IsBusy() {
		var status string
		if a.chatModel.IsStreaming() {
			status = "receiving"
		} else {
			status = "working"
		}
		title = title + a.styles.Info.Render(" · "+status)
	}

	if a.chatModel != nil && a.chatModel.GetTopic() != "" {
		topic := a.chatModel.GetTopic()
		topicPart := a.styles.Help.Render("topic: ") + a.styles.TopicCurrent.Render(topic)

		contentWidth := a.width - 4 // Header Width(w-2) minus Padding(0,1) = w-4
		titleW := lipgloss.Width(title)
		topicW := lipgloss.Width(topicPart)
		gap := contentWidth - titleW - topicW
		if gap < 2 {
			gap = 2
		}
		title = title + strings.Repeat(" ", gap) + topicPart
	}

	return a.styles.Header.Width(a.width - 2).Render(title)
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

// renderHelp renders the help text with key bindings.
func (a App) renderHelp() string {
	bindings := []string{
		a.styles.RenderKeyBinding("ctrl+n", "new"),
		a.styles.RenderKeyBinding("ctrl+h", "history"),
		a.styles.RenderKeyBinding("ctrl+t", "topics"),
		a.styles.RenderKeyBinding("ctrl+d", "debug"),
		a.styles.RenderKeyBinding("esc", "quit"),
	}
	return a.styles.Help.Render(strings.Join(bindings, "  "))
}

// computeChatStartY returns the terminal row where the chat model's content
// begins, accounting for the header and optional error bar.
func (a App) computeChatStartY() int {
	y := strings.Count(a.renderHeader(), "\n") + 1 // header + trailing \n in View
	if a.err != nil {
		y += strings.Count(a.renderError(), "\n") + 1
	}
	return y
}

// handleNewConversation creates a new conversation.
func (a App) handleNewConversation() (tea.Model, tea.Cmd) {
	if a.engine == nil {
		return a, nil
	}
	a.activeOverlay = nil // Close any open overlay

	return a, func() tea.Msg {
		ctx := a.ctx
		conv, err := a.engine.NewConversation(ctx, "New Chat", "")
		if err != nil {
			return errMsg{err: err}
		}
		return ConversationCreatedMsg{
			ConvID: conv.ID,
			Title:  conv.Title,
		}
	}
}

// --- Accessor methods ---

func (a *App) SetConversation(convID string)          { a.convID = convID }
func (a *App) GetConversation() string                { return a.convID }
func (a *App) SetError(err error)                     { a.err = err }
func (a *App) ClearError()                            { a.err = nil }
func (a *App) Nostop() *nostop.Nostop                 { return a.engine }
func (a *App) Tracker() *topic.TopicTracker           { return a.tracker }
func (a *App) Archiver() *nostop.Archiver             { return a.archiver }
func (a *App) ContextManager() *nostop.ContextManager { return a.contextMgr }
func (a *App) DebugModel() *DebugModel                { return a.debugModel }
func (a *App) SetTracker(tracker *topic.TopicTracker) { a.tracker = tracker }

func (a *App) SetArchiver(archiver *nostop.Archiver) {
	a.archiver = archiver
	if a.chatModel != nil {
		a.chatModel.SetArchiver(archiver)
	}
}

func (a *App) SetContextManager(mgr *nostop.ContextManager) {
	a.contextMgr = mgr
	if a.debugModel != nil {
		a.debugModel = NewDebugModel(mgr, a.tracker, a.convID, a.ctx, a.width-4, a.height-6)
	}
}

// AddArchiveEvent adds an archive event to the debug model's history.
func (a *App) AddArchiveEvent(topicName string, tokens int, isRestore bool) {
	if a.debugModel != nil {
		a.debugModel.AddArchiveEvent(topicName, tokens, isRestore)
	}
}

// --- Message types ---

type errMsg struct{ err error }
type clearErrMsg struct{}

// --- Helper functions ---

func truncate(s string, length int) string {
	if len(s) <= length {
		return s
	}
	if length <= 3 {
		return s[:length]
	}
	return s[:length-3] + "..."
}

func (a *App) loadChatMessages() tea.Cmd {
	return func() tea.Msg {
		if a.convID == "" || a.engine == nil || a.chatModel == nil {
			return nil
		}
		ctx := a.ctx
		if err := a.chatModel.LoadMessages(ctx); err != nil {
			return errMsg{err: err}
		}
		return nil
	}
}
