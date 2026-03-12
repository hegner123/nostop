package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/user/rlm/internal/storage"
)

// HistoryModel represents the conversation history browser view.
type HistoryModel struct {
	storage       *storage.SQLite
	conversations []storage.Conversation
	messageCounts map[string]int // conversation ID -> message count
	topicCounts   map[string]int // conversation ID -> topic count
	list          list.Model
	selected      int
	width         int
	height        int
	loading       bool
	err           error
	confirmDelete bool   // whether we're in delete confirmation mode
	deleteTarget  string // ID of conversation to delete
	styles        Styles
}

// conversationItem implements list.Item for displaying conversations.
type conversationItem struct {
	conv         storage.Conversation
	messageCount int
	topicCount   int
}

func (i conversationItem) Title() string {
	title := i.conv.Title
	if title == "" {
		title = "Untitled Conversation"
	}
	return title
}

func (i conversationItem) Description() string {
	dateStr := i.conv.CreatedAt.Format("2006-01-02")
	msgPlural := "messages"
	if i.messageCount == 1 {
		msgPlural = "message"
	}
	topicPlural := "topics"
	if i.topicCount == 1 {
		topicPlural = "topic"
	}
	return fmt.Sprintf("Created: %s | %d %s | %d %s", dateStr, i.messageCount, msgPlural, i.topicCount, topicPlural)
}

func (i conversationItem) FilterValue() string {
	return i.conv.Title
}

// Custom messages for history view.

// ConversationsLoadedMsg indicates conversations have been loaded from storage.
type ConversationsLoadedMsg struct {
	Conversations []storage.Conversation
	MessageCounts map[string]int
	TopicCounts   map[string]int
}

// ConversationsLoadErrorMsg indicates an error loading conversations.
type ConversationsLoadErrorMsg struct {
	Err error
}

// ConversationSelectedMsg indicates a conversation was selected.
type ConversationSelectedMsg struct {
	ConvID string
}

// ConversationDeletedMsg indicates a conversation was deleted.
type ConversationDeletedMsg struct {
	ConvID string
}

// ConversationDeleteErrorMsg indicates an error deleting a conversation.
type ConversationDeleteErrorMsg struct {
	Err error
}

// NewHistoryModel creates a new HistoryModel instance.
func NewHistoryModel(store *storage.SQLite, width, height int) *HistoryModel {
	// Create delegate for list items
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true

	// Create an empty list - will be populated when conversations load
	l := list.New([]list.Item{}, delegate, width-4, height-8)
	l.Title = "Conversation History"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false) // We render our own help

	styles := DefaultStyles()

	// Customize list styles
	l.Styles.Title = styles.PanelTitle
	l.Styles.FilterPrompt = styles.InputPrompt
	l.Styles.FilterCursor = styles.InputPrompt

	return &HistoryModel{
		storage:       store,
		list:          l,
		width:         width,
		height:        height,
		loading:       true,
		messageCounts: make(map[string]int),
		topicCounts:   make(map[string]int),
		styles:        styles,
	}
}

// Init implements tea.Model.
func (m *HistoryModel) Init() tea.Cmd {
	return m.loadConversations()
}

// loadConversations returns a command to load conversations from storage.
func (m *HistoryModel) loadConversations() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conversations, err := m.storage.ListConversations(ctx, 100, 0)
		if err != nil {
			return ConversationsLoadErrorMsg{Err: err}
		}

		messageCounts := make(map[string]int)
		topicCounts := make(map[string]int)

		for _, conv := range conversations {
			// Get message count
			messages, err := m.storage.ListMessages(ctx, conv.ID)
			if err == nil {
				messageCounts[conv.ID] = len(messages)
			}

			// Get topic count
			topics, err := m.storage.ListTopics(ctx, conv.ID)
			if err == nil {
				topicCounts[conv.ID] = len(topics)
			}
		}

		return ConversationsLoadedMsg{
			Conversations: conversations,
			MessageCounts: messageCounts,
			TopicCounts:   topicCounts,
		}
	}
}

// deleteConversation returns a command to delete a conversation.
func (m *HistoryModel) deleteConversation(convID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := m.storage.DeleteConversation(ctx, convID)
		if err != nil {
			return ConversationDeleteErrorMsg{Err: err}
		}

		return ConversationDeletedMsg{ConvID: convID}
	}
}

// Update implements tea.Model.
func (m *HistoryModel) Update(msg tea.Msg) (*HistoryModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case ConversationsLoadedMsg:
		m.loading = false
		m.conversations = msg.Conversations
		m.messageCounts = msg.MessageCounts
		m.topicCounts = msg.TopicCounts
		m.updateListItems()
		return m, nil

	case ConversationsLoadErrorMsg:
		m.loading = false
		m.err = msg.Err
		return m, nil

	case ConversationDeletedMsg:
		// Remove from local list
		for i, conv := range m.conversations {
			if conv.ID == msg.ConvID {
				m.conversations = append(m.conversations[:i], m.conversations[i+1:]...)
				delete(m.messageCounts, msg.ConvID)
				delete(m.topicCounts, msg.ConvID)
				break
			}
		}
		m.confirmDelete = false
		m.deleteTarget = ""
		m.updateListItems()
		return m, nil

	case ConversationDeleteErrorMsg:
		m.err = msg.Err
		m.confirmDelete = false
		m.deleteTarget = ""
		return m, nil

	case tea.KeyMsg:
		// Handle delete confirmation mode
		if m.confirmDelete {
			switch msg.String() {
			case "y", "Y":
				// Confirm deletion
				cmd := m.deleteConversation(m.deleteTarget)
				return m, cmd
			case "n", "N", "esc":
				// Cancel deletion
				m.confirmDelete = false
				m.deleteTarget = ""
				return m, nil
			}
			return m, nil
		}

		// Normal key handling
		switch msg.String() {
		case "enter":
			// Select the current conversation
			if item, ok := m.list.SelectedItem().(conversationItem); ok {
				return m, func() tea.Msg {
					return ConversationSelectedMsg{ConvID: item.conv.ID}
				}
			}

		case "d", "delete", "backspace":
			// Initiate delete with confirmation
			if item, ok := m.list.SelectedItem().(conversationItem); ok {
				m.confirmDelete = true
				m.deleteTarget = item.conv.ID
				return m, nil
			}

		case "r":
			// Refresh the list
			m.loading = true
			return m, m.loadConversations()
		}
	}

	// Pass other messages to the list
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// updateListItems updates the list items from the conversations slice.
func (m *HistoryModel) updateListItems() {
	items := make([]list.Item, len(m.conversations))
	for i, conv := range m.conversations {
		items[i] = conversationItem{
			conv:         conv,
			messageCount: m.messageCounts[conv.ID],
			topicCount:   m.topicCounts[conv.ID],
		}
	}
	m.list.SetItems(items)
}

// View implements tea.Model.
func (m *HistoryModel) View() string {
	var b strings.Builder

	if m.loading {
		b.WriteString(m.styles.SystemMessage.Render("Loading conversations..."))
		return b.String()
	}

	if m.err != nil {
		label := m.styles.ErrorLabel.Render("ERROR")
		errMsg := m.styles.Error.Render(m.err.Error())
		b.WriteString(label + " " + errMsg + "\n\n")
	}

	// Show delete confirmation if active
	if m.confirmDelete {
		// Find the conversation being deleted
		var title string
		for _, conv := range m.conversations {
			if conv.ID == m.deleteTarget {
				title = conv.Title
				if title == "" {
					title = "Untitled Conversation"
				}
				break
			}
		}

		confirmBox := m.styles.Panel.
			Width(m.width - 10).
			BorderForeground(lipgloss.Color("196")).
			Render(fmt.Sprintf(
				"%s\n\nDelete conversation \"%s\"?\n\nThis action cannot be undone.\n\n%s  %s",
				m.styles.WarningLabel.Render("CONFIRM DELETE"),
				title,
				m.styles.HelpKey.Render("y")+" yes",
				m.styles.HelpKey.Render("n")+" no",
			))
		b.WriteString(confirmBox)
		return b.String()
	}

	if len(m.conversations) == 0 {
		emptyMsg := m.styles.Panel.
			Width(m.width - 6).
			Height(m.height - 10).
			Render("No conversations yet.\n\nPress Ctrl+N to start a new conversation.")
		b.WriteString(emptyMsg)
		return b.String()
	}

	// Render the list
	b.WriteString(m.list.View())

	return b.String()
}

// SetSize updates the model dimensions.
func (m *HistoryModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.list.SetSize(width-4, height-8)
}

// SelectedConversation returns the currently selected conversation, if any.
func (m *HistoryModel) SelectedConversation() *storage.Conversation {
	if item, ok := m.list.SelectedItem().(conversationItem); ok {
		return &item.conv
	}
	return nil
}

// Refresh reloads conversations from storage.
func (m *HistoryModel) Refresh() tea.Cmd {
	m.loading = true
	return m.loadConversations()
}

// RenderHelp returns the help text for the history view.
func (m *HistoryModel) RenderHelp() string {
	if m.confirmDelete {
		return ""
	}

	bindings := []string{
		m.styles.RenderKeyBinding("j/k", "navigate"),
		m.styles.RenderKeyBinding("enter", "select"),
		m.styles.RenderKeyBinding("d", "delete"),
		m.styles.RenderKeyBinding("/", "filter"),
		m.styles.RenderKeyBinding("r", "refresh"),
	}
	return strings.Join(bindings, "  ")
}
