package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/hegner123/nostop/internal/storage"
)

// HistoryModel represents the topic history browser view.
type HistoryModel struct {
	ctx           context.Context
	storage       *storage.SQLite
	topics        []storage.TopicWithCounts
	list          list.Model
	selected      int
	width         int
	height        int
	loading       bool
	err           error
	confirmDelete bool   // whether we're in delete confirmation mode
	deleteTarget  string // ID of topic to delete
	styles        Styles
}

// topicItem implements list.Item for displaying topics.
type topicItem struct {
	topic storage.TopicWithCounts
}

func (i topicItem) Title() string {
	name := i.topic.Name
	if name == "" {
		name = "Untitled Topic"
	}
	// Add status indicators
	if i.topic.IsCurrent {
		name = "★ " + name
	}
	if i.topic.IsArchived() {
		name = name + " [archived]"
	}
	return name
}

func (i topicItem) Description() string {
	dateStr := i.topic.UpdatedAt.Format("2006-01-02 15:04")
	msgPlural := "messages"
	if i.topic.MessageCount == 1 {
		msgPlural = "message"
	}
	tokenStr := formatTokenCount(i.topic.TokenCount)
	return fmt.Sprintf("Updated: %s | %d %s | %s tokens", dateStr, i.topic.MessageCount, msgPlural, tokenStr)
}

func (i topicItem) FilterValue() string {
	return i.topic.Name
}

// Custom messages for history view.

// TopicsHistoryLoadedMsg indicates topics have been loaded from storage.
type TopicsHistoryLoadedMsg struct {
	Topics []storage.TopicWithCounts
}

// TopicsHistoryLoadErrorMsg indicates an error loading topics.
type TopicsHistoryLoadErrorMsg struct {
	Err error
}

// TopicSelectedMsg indicates a topic was selected.
type TopicSelectedMsg struct {
	TopicID        string
	ConversationID string
}

// TopicDeletedMsg indicates a topic was deleted.
type TopicDeletedMsg struct {
	TopicID string
}

// TopicDeleteErrorMsg indicates an error deleting a topic.
type TopicDeleteErrorMsg struct {
	Err error
}

// NewHistoryModel creates a new HistoryModel instance.
func NewHistoryModel(store *storage.SQLite, ctx context.Context, width, height int) *HistoryModel {
	// Create delegate for list items
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true

	// Create an empty list - will be populated when topics load
	l := list.New([]list.Item{}, delegate, width-4, height-8)
	l.Title = "Topic History"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false) // We render our own help

	styles := DefaultStyles()

	// Customize list styles
	l.Styles.Title = styles.PanelTitle

	return &HistoryModel{
		ctx:     ctx,
		storage: store,
		list:    l,
		width:   width,
		height:  height,
		loading: true,
		styles:  styles,
	}
}

// Init implements tea.Model.
func (m *HistoryModel) Init() tea.Cmd {
	return m.loadTopics()
}

// loadTopics returns a command to load topics from storage.
func (m *HistoryModel) loadTopics() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()

		topics, err := m.storage.ListAllTopics(ctx, 100, 0)
		if err != nil {
			return TopicsHistoryLoadErrorMsg{Err: err}
		}

		return TopicsHistoryLoadedMsg{Topics: topics}
	}
}

// deleteTopic returns a command to delete a topic.
func (m *HistoryModel) deleteTopic(topicID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()

		err := m.storage.DeleteTopic(ctx, topicID)
		if err != nil {
			return TopicDeleteErrorMsg{Err: err}
		}

		return TopicDeletedMsg{TopicID: topicID}
	}
}

// Update implements tea.Model.
func (m *HistoryModel) Update(msg tea.Msg) (*HistoryModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case TopicsHistoryLoadedMsg:
		m.loading = false
		m.topics = msg.Topics
		m.updateListItems()
		return m, nil

	case TopicsHistoryLoadErrorMsg:
		m.loading = false
		m.err = msg.Err
		return m, nil

	case TopicDeletedMsg:
		// Remove from local list
		for i, t := range m.topics {
			if t.ID == msg.TopicID {
				m.topics = append(m.topics[:i], m.topics[i+1:]...)
				break
			}
		}
		m.confirmDelete = false
		m.deleteTarget = ""
		m.updateListItems()
		return m, nil

	case TopicDeleteErrorMsg:
		m.err = msg.Err
		m.confirmDelete = false
		m.deleteTarget = ""
		return m, nil

	case tea.KeyPressMsg:
		// Handle delete confirmation mode
		if m.confirmDelete {
			switch msg.String() {
			case "y", "Y":
				// Confirm deletion
				cmd := m.deleteTopic(m.deleteTarget)
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
			// Select the current topic
			if item, ok := m.list.SelectedItem().(topicItem); ok {
				return m, func() tea.Msg {
					return TopicSelectedMsg{
						TopicID:        item.topic.ID,
						ConversationID: item.topic.ConversationID,
					}
				}
			}

		case "d", "delete", "backspace":
			// Initiate delete with confirmation
			if item, ok := m.list.SelectedItem().(topicItem); ok {
				m.confirmDelete = true
				m.deleteTarget = item.topic.ID
				return m, nil
			}

		case "r":
			// Refresh the list
			m.loading = true
			return m, m.loadTopics()
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

// updateListItems updates the list items from the topics slice.
func (m *HistoryModel) updateListItems() {
	items := make([]list.Item, len(m.topics))
	for i, t := range m.topics {
		items[i] = topicItem{topic: t}
	}
	m.list.SetItems(items)
}

// View implements tea.Model.
func (m *HistoryModel) View() string {
	var b strings.Builder

	if m.loading {
		b.WriteString(m.styles.SystemMessage.Render("Loading topics..."))
		return b.String()
	}

	if m.err != nil {
		label := m.styles.ErrorLabel.Render("ERROR")
		errMsg := m.styles.Error.Render(m.err.Error())
		b.WriteString(label + " " + errMsg + "\n\n")
	}

	// Show delete confirmation if active
	if m.confirmDelete {
		// Find the topic being deleted
		var name string
		for _, t := range m.topics {
			if t.ID == m.deleteTarget {
				name = t.Name
				if name == "" {
					name = "Untitled Topic"
				}
				break
			}
		}

		confirmBox := m.styles.Panel.
			Width(m.width - 10).
			BorderForeground(lipgloss.Color("196")).
			Render(fmt.Sprintf(
				"%s\n\nDelete topic \"%s\"?\n\nThis action cannot be undone.\n\n%s  %s",
				m.styles.WarningLabel.Render("CONFIRM DELETE"),
				name,
				m.styles.HelpKey.Render("y")+" yes",
				m.styles.HelpKey.Render("n")+" no",
			))
		b.WriteString(confirmBox)
		return b.String()
	}

	if len(m.topics) == 0 {
		emptyMsg := m.styles.Panel.
			Width(m.width - 6).
			Height(m.height - 10).
			Render("No topics yet.\n\nStart chatting to create your first topic.")
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

// SelectedTopic returns the currently selected topic, if any.
func (m *HistoryModel) SelectedTopic() *storage.TopicWithCounts {
	if item, ok := m.list.SelectedItem().(topicItem); ok {
		return &item.topic
	}
	return nil
}

// Refresh reloads topics from storage.
func (m *HistoryModel) Refresh() tea.Cmd {
	m.loading = true
	return m.loadTopics()
}

// OverlayUpdate implements ModalOverlay.
func (m *HistoryModel) OverlayUpdate(msg tea.KeyMsg) (ModalOverlay, tea.Cmd) {
	if msg.String() == "esc" {
		// Cancel delete confirmation first
		if m.confirmDelete {
			m.confirmDelete = false
			m.deleteTarget = ""
			return m, nil
		}
		// Cancel list filter first
		if m.list.FilterState() == list.Filtering {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}
		// Close overlay
		return nil, nil
	}
	m, cmd := m.Update(msg)
	return m, cmd
}

// OverlayView implements ModalOverlay.
func (m *HistoryModel) OverlayView(width, height int) string {
	dlgW := width * 3 / 4
	dlgH := height * 3 / 4
	if dlgW < 50 {
		dlgW = 50
	}
	if dlgH < 15 {
		dlgH = 15
	}

	// Content area inside border (2) + padding (4 horiz, 2 vert)
	contentW := dlgW - 6
	contentH := dlgH - 4
	m.SetSize(contentW, contentH)

	style := overlayStyle().Width(dlgW).Height(dlgH)
	return style.Render(m.View())
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
