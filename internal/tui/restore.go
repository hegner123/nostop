package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/hegner123/nostop/internal/storage"
)

// RestorePromptResult is sent when the restore prompt has been answered.
type RestorePromptResult struct {
	Topic   *storage.Topic
	Restore bool
	Dismiss bool
	Message string // Original message that triggered the prompt
}

// RestoreNotification represents a notification about a completed restore.
type RestoreNotification struct {
	TopicName  string
	TokenCount int
	Visible    bool
	ExpiresAt  int64 // Unix timestamp when notification should hide
}

// RestorePrompt manages the UI for prompting users to restore archived topics.
type RestorePrompt struct {
	topics   []*storage.Topic // Topics that may need restoration
	message  string           // User's message that triggered this
	visible  bool
	selected int // Which topic is selected (for multiple matches)
	width    int
	styles   Styles
}

// NewRestorePrompt creates a new RestorePrompt for the given topics.
func NewRestorePrompt(topics []*storage.Topic, message string, width int) *RestorePrompt {
	return &RestorePrompt{
		topics:   topics,
		message:  message,
		visible:  len(topics) > 0,
		selected: 0,
		width:    width,
		styles:   DefaultStyles(),
	}
}

// SetTopics updates the topics to prompt for restoration.
func (rp *RestorePrompt) SetTopics(topics []*storage.Topic, message string) {
	rp.topics = topics
	rp.message = message
	rp.visible = len(topics) > 0
	rp.selected = 0
}

// Clear hides the prompt and clears the topics.
func (rp *RestorePrompt) Clear() {
	rp.topics = nil
	rp.message = ""
	rp.visible = false
	rp.selected = 0
}

// IsVisible returns whether the prompt should be displayed.
func (rp *RestorePrompt) IsVisible() bool {
	return rp.visible && len(rp.topics) > 0
}

// GetSelectedTopic returns the currently selected topic.
func (rp *RestorePrompt) GetSelectedTopic() *storage.Topic {
	if rp.selected >= 0 && rp.selected < len(rp.topics) {
		return rp.topics[rp.selected]
	}
	return nil
}

// GetMessage returns the user's message that triggered the prompt.
func (rp *RestorePrompt) GetMessage() string {
	return rp.message
}

// SetWidth updates the width for rendering.
func (rp *RestorePrompt) SetWidth(width int) {
	rp.width = width
}

// View renders the restore prompt.
func (rp *RestorePrompt) View() string {
	if !rp.IsVisible() {
		return ""
	}

	var b strings.Builder

	// Create styled prompt box
	promptStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("214")). // Orange for attention
		Padding(1, 2).
		Width(rp.width - 10)

	// Header with info icon
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("214"))

	b.WriteString(headerStyle.Render("Archived Content Detected"))
	b.WriteString("\n\n")

	// Show which topic(s) matched
	if len(rp.topics) == 1 {
		topic := rp.topics[0]
		b.WriteString(fmt.Sprintf("Your message might relate to the archived topic:\n"))
		b.WriteString("\n")
		b.WriteString(rp.renderTopicPreview(topic, true))
	} else {
		b.WriteString(fmt.Sprintf("Your message might relate to %d archived topics:\n", len(rp.topics)))
		b.WriteString("\n")
		for i, topic := range rp.topics {
			isSelected := i == rp.selected
			b.WriteString(rp.renderTopicPreview(topic, isSelected))
			b.WriteString("\n")
		}
	}

	// Action prompt
	b.WriteString("\n")
	actionStyle := lipgloss.NewStyle().
		Italic(true).
		Foreground(lipgloss.Color("252"))

	if len(rp.topics) == 1 {
		b.WriteString(actionStyle.Render("Restore this topic to include its context? "))
	} else {
		b.WriteString(actionStyle.Render("Restore selected topic? (j/k to navigate) "))
	}

	// Key bindings
	keyStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("39")) // Cyan

	b.WriteString(keyStyle.Render("[y]"))
	b.WriteString(rp.styles.Help.Render(" restore  "))
	b.WriteString(keyStyle.Render("[n]"))
	b.WriteString(rp.styles.Help.Render(" skip  "))
	b.WriteString(keyStyle.Render("[esc]"))
	b.WriteString(rp.styles.Help.Render(" cancel"))

	return promptStyle.Render(b.String())
}

// renderTopicPreview renders a single topic preview entry.
func (rp *RestorePrompt) renderTopicPreview(topic *storage.Topic, isSelected bool) string {
	var b strings.Builder

	// Selection indicator
	prefix := "  "
	if isSelected {
		prefix = rp.styles.TopicCurrent.Render("> ")
	}

	// Topic name
	nameStyle := rp.styles.TopicArchived
	if isSelected {
		nameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)
	}

	b.WriteString(prefix)
	b.WriteString(nameStyle.Render(topic.Name))

	// Token count (cost of restoration)
	tokenStr := formatTokenCount(topic.TokenCount)
	b.WriteString(rp.styles.Help.Render(fmt.Sprintf(" (%s tokens)", tokenStr)))

	b.WriteString("\n")

	// Keywords preview (indented)
	if len(topic.Keywords) > 0 {
		indent := "      "
		keywordsStr := strings.Join(topic.Keywords, ", ")
		if len(keywordsStr) > 40 {
			keywordsStr = keywordsStr[:37] + "..."
		}
		b.WriteString(indent)
		b.WriteString(rp.styles.Help.Render("Keywords: " + keywordsStr))
	}

	return b.String()
}

// HandleKey processes a key event and returns the result.
// Returns (restore, dismiss, handled) where:
// - restore: true if user chose to restore
// - dismiss: true if user dismissed the prompt
// - handled: true if the key was consumed by the prompt
func (rp *RestorePrompt) HandleKey(key string) (restore bool, dismiss bool, handled bool) {
	if !rp.IsVisible() {
		return false, false, false
	}

	switch key {
	case "y", "Y":
		return true, false, true
	case "n", "N":
		return false, true, true
	case "esc":
		rp.Clear()
		return false, true, true
	case "j", "down":
		if len(rp.topics) > 1 && rp.selected < len(rp.topics)-1 {
			rp.selected++
		}
		return false, false, true
	case "k", "up":
		if len(rp.topics) > 1 && rp.selected > 0 {
			rp.selected--
		}
		return false, false, true
	}

	return false, false, false
}

// RestoreConfirmDialog manages confirmation for restoring topics from the topics view.
type RestoreConfirmDialog struct {
	topic       *storage.Topic
	visible     bool
	width       int
	styles      Styles
	showPreview bool
}

// NewRestoreConfirmDialog creates a new confirmation dialog.
func NewRestoreConfirmDialog(width int) *RestoreConfirmDialog {
	return &RestoreConfirmDialog{
		width:       width,
		styles:      DefaultStyles(),
		showPreview: true,
	}
}

// Show displays the confirmation dialog for the given topic.
func (rcd *RestoreConfirmDialog) Show(topic *storage.Topic) {
	rcd.topic = topic
	rcd.visible = true
}

// Hide hides the confirmation dialog.
func (rcd *RestoreConfirmDialog) Hide() {
	rcd.topic = nil
	rcd.visible = false
}

// IsVisible returns whether the dialog is visible.
func (rcd *RestoreConfirmDialog) IsVisible() bool {
	return rcd.visible && rcd.topic != nil
}

// GetTopic returns the topic being confirmed.
func (rcd *RestoreConfirmDialog) GetTopic() *storage.Topic {
	return rcd.topic
}

// SetWidth updates the width for rendering.
func (rcd *RestoreConfirmDialog) SetWidth(width int) {
	rcd.width = width
}

// TogglePreview toggles the preview display.
func (rcd *RestoreConfirmDialog) TogglePreview() {
	rcd.showPreview = !rcd.showPreview
}

// View renders the confirmation dialog.
func (rcd *RestoreConfirmDialog) View() string {
	if !rcd.IsVisible() {
		return ""
	}

	var b strings.Builder

	// Create styled dialog box
	dialogStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("82")). // Green for confirmation
		Padding(1, 2).
		Width(rcd.width - 10)

	// Header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("82"))

	b.WriteString(headerStyle.Render("Restore Topic"))
	b.WriteString("\n\n")

	// Topic details
	b.WriteString(rcd.styles.Info.Render("Topic: "))
	b.WriteString(rcd.styles.TopicActive.Render(rcd.topic.Name))
	b.WriteString("\n\n")

	// Token cost
	tokenStr := formatTokenCount(rcd.topic.TokenCount)
	b.WriteString(rcd.styles.Info.Render("Token Cost: "))
	b.WriteString(rcd.styles.DebugValue.Render(tokenStr))
	b.WriteString("\n")

	// Archive time
	if rcd.topic.ArchivedAt != nil {
		archivedAgo := formatTimeAgo(rcd.topic.ArchivedAt)
		b.WriteString(rcd.styles.Info.Render("Archived: "))
		b.WriteString(rcd.styles.Help.Render(archivedAgo))
		b.WriteString("\n")
	}

	// Keywords
	if len(rcd.topic.Keywords) > 0 {
		b.WriteString(rcd.styles.Info.Render("Keywords: "))
		keywordsStr := strings.Join(rcd.topic.Keywords, ", ")
		if len(keywordsStr) > 50 {
			keywordsStr = keywordsStr[:47] + "..."
		}
		b.WriteString(rcd.styles.Help.Render(keywordsStr))
		b.WriteString("\n")
	}

	// Context impact warning
	b.WriteString("\n")
	warningStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")).
		Italic(true)
	b.WriteString(warningStyle.Render(fmt.Sprintf("This will add %s tokens to active context.", tokenStr)))
	b.WriteString("\n\n")

	// Action prompt
	b.WriteString("Restore this topic? ")

	// Key bindings
	keyStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("39"))

	b.WriteString(keyStyle.Render("[y]"))
	b.WriteString(rcd.styles.Help.Render(" yes  "))
	b.WriteString(keyStyle.Render("[n/esc]"))
	b.WriteString(rcd.styles.Help.Render(" cancel"))

	return dialogStyle.Render(b.String())
}

// HandleKey processes a key event and returns (confirmed, cancelled, handled).
func (rcd *RestoreConfirmDialog) HandleKey(key string) (confirmed bool, cancelled bool, handled bool) {
	if !rcd.IsVisible() {
		return false, false, false
	}

	switch key {
	case "y", "Y", "enter":
		return true, false, true
	case "n", "N", "esc", "q":
		rcd.Hide()
		return false, true, true
	case "p":
		rcd.TogglePreview()
		return false, false, true
	}

	return false, false, false
}

// NotificationType identifies the kind of notification.
type NotificationType int

const (
	NotifyRestore    NotificationType = iota // Archived topic restored
	NotifyArchive                            // Active topic archived
	NotifyTopicNew                           // New topic identified
	NotifyTopicShift                         // Topic shift detected
	NotifyInfo                               // General info
)

// Notification represents a transient message shown in the chat view.
type Notification struct {
	Type       NotificationType
	Message    string
	TopicName  string
	TokenCount int
	Visible    bool
}

// NotificationManager manages notifications in the chat view.
type NotificationManager struct {
	notifications []Notification
	maxVisible    int
	styles        Styles
}

// NewNotificationManager creates a new notification manager.
func NewNotificationManager() *NotificationManager {
	return &NotificationManager{
		notifications: make([]Notification, 0),
		maxVisible:    3,
		styles:        DefaultStyles(),
	}
}

// push adds a notification, evicting the oldest if at capacity.
func (nm *NotificationManager) push(n Notification) {
	if len(nm.notifications) >= nm.maxVisible {
		nm.notifications = nm.notifications[1:]
	}
	nm.notifications = append(nm.notifications, n)
}

// AddRestoreNotification adds a notification about a restored topic.
func (nm *NotificationManager) AddRestoreNotification(topicName string, tokenCount int) {
	nm.push(Notification{
		Type:       NotifyRestore,
		TopicName:  topicName,
		TokenCount: tokenCount,
		Visible:    true,
	})
}

// AddArchiveNotification adds a notification about an archived topic.
func (nm *NotificationManager) AddArchiveNotification(topicName string, tokenCount int) {
	nm.push(Notification{
		Type:       NotifyArchive,
		TopicName:  topicName,
		TokenCount: tokenCount,
		Visible:    true,
	})
}

// AddTopicNotification adds a notification about a new or shifted topic.
func (nm *NotificationManager) AddTopicNotification(topicName string, isShift bool) {
	ntype := NotifyTopicNew
	if isShift {
		ntype = NotifyTopicShift
	}
	nm.push(Notification{
		Type:      ntype,
		TopicName: topicName,
		Visible:   true,
	})
}

// AddInfo adds a general informational notification.
func (nm *NotificationManager) AddInfo(message string) {
	nm.push(Notification{
		Type:    NotifyInfo,
		Message: message,
		Visible: true,
	})
}

// Clear removes all notifications.
func (nm *NotificationManager) Clear() {
	nm.notifications = nil
}

// HasNotifications returns true if there are visible notifications.
func (nm *NotificationManager) HasNotifications() bool {
	return len(nm.notifications) > 0
}

// View renders all visible notifications.
func (nm *NotificationManager) View() string {
	if len(nm.notifications) == 0 {
		return ""
	}

	var b strings.Builder

	for _, n := range nm.notifications {
		if !n.Visible {
			continue
		}

		var style lipgloss.Style
		var msg string

		switch n.Type {
		case NotifyRestore:
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Italic(true)
			tokenStr := formatTokenCount(n.TokenCount)
			msg = fmt.Sprintf("Restored topic '%s' (%s tokens)", n.TopicName, tokenStr)
		case NotifyArchive:
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Italic(true)
			tokenStr := formatTokenCount(n.TokenCount)
			msg = fmt.Sprintf("Archived topic '%s' (%s tokens freed)", n.TopicName, tokenStr)
		case NotifyTopicNew:
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Italic(true)
			msg = fmt.Sprintf("Topic: %s", n.TopicName)
		case NotifyTopicShift:
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Italic(true)
			msg = fmt.Sprintf("Topic shifted to: %s", n.TopicName)
		case NotifyInfo:
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Italic(true)
			msg = n.Message
		}

		b.WriteString(style.Render(msg))
		b.WriteString("\n")
	}

	return b.String()
}

// RestoreResult holds the result of a topic restoration operation.
type RestoreResult struct {
	Success    bool
	TopicID    string
	TopicName  string
	TokenCount int
	Error      error
}

// RestoreRequestMsg is sent when a restore is requested from the prompt.
type RestoreRequestMsg struct {
	Topic   *storage.Topic
	Message string // The original user message
}

// TopicRestoreCompleteMsg is sent when a topic restore completes in the chat context.
type TopicRestoreCompleteMsg struct {
	Topic *storage.Topic
	Error error
}
