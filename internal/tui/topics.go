package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hegner123/nostop/internal/storage"
	"github.com/hegner123/nostop/internal/topic"
	"github.com/hegner123/nostop/pkg/nostop"
)

// TopicsLoadedMsg is sent when topics have been loaded.
type TopicsLoadedMsg struct {
	Topics   []*storage.Topic
	Archived []*storage.Topic
}

// TopicRestoredMsg is sent when an archived topic has been restored.
type TopicRestoredMsg struct {
	Topic *storage.Topic
}

// topicsLoadErrorMsg is sent when loading topics fails.
type topicsLoadErrorMsg struct {
	err error
}

// topicRestoreErrorMsg is sent when restoring a topic fails.
type topicRestoreErrorMsg struct {
	err error
}

// TopicsModel manages the topics overview view.
type TopicsModel struct {
	tracker       *topic.TopicTracker
	archiver      *nostop.Archiver
	convID        string
	topics        []*storage.Topic
	archived      []*storage.Topic
	selected      int
	showArchived  bool
	width         int
	height        int
	err           error
	styles        Styles
	loading       bool
	confirmDialog *RestoreConfirmDialog // Confirmation dialog for restoration
}

// NewTopicsModel creates a new TopicsModel instance.
func NewTopicsModel(tracker *topic.TopicTracker, archiver *nostop.Archiver, convID string, width, height int) *TopicsModel {
	return &TopicsModel{
		tracker:       tracker,
		archiver:      archiver,
		convID:        convID,
		topics:        make([]*storage.Topic, 0),
		archived:      make([]*storage.Topic, 0),
		selected:      0,
		showArchived:  false,
		width:         width,
		height:        height,
		styles:        DefaultStyles(),
		loading:       true,
		confirmDialog: NewRestoreConfirmDialog(width),
	}
}

// Init implements tea.Model. It loads topics for the conversation.
func (m TopicsModel) Init() tea.Cmd {
	return m.loadTopics()
}

// loadTopics creates a command to load topics from the tracker and archiver.
func (m TopicsModel) loadTopics() tea.Cmd {
	return func() tea.Msg {
		if m.tracker == nil {
			return topicsLoadErrorMsg{err: fmt.Errorf("topic tracker not initialized")}
		}

		// Reload topics from the database so we have fresh state.
		// Without this, GetTopics() returns stale in-memory data that
		// may belong to a different conversation or a prior session.
		if m.convID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := m.tracker.LoadTopics(ctx, m.convID); err != nil {
				return topicsLoadErrorMsg{err: fmt.Errorf("failed to reload topics: %w", err)}
			}
		}

		// Get active topics from tracker
		activeTopics := m.tracker.GetTopics()

		// Get archived topics from archiver if available
		var archivedTopics []storage.Topic
		if m.archiver != nil && m.convID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var err error
			archivedTopics, err = m.archiver.GetArchivedTopics(ctx, m.convID)
			if err != nil {
				// Log but don't fail - we can still show active topics
				archivedTopics = nil
			}
		}

		// Convert archived slice to pointers
		archivedPtrs := make([]*storage.Topic, len(archivedTopics))
		for i := range archivedTopics {
			archivedPtrs[i] = &archivedTopics[i]
		}

		return TopicsLoadedMsg{
			Topics:   activeTopics,
			Archived: archivedPtrs,
		}
	}
}

// Update implements tea.Model. It handles messages and key events.
func (m TopicsModel) Update(msg tea.Msg) (TopicsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case TopicsLoadedMsg:
		m.topics = msg.Topics
		m.archived = msg.Archived
		m.loading = false
		m.err = nil
		// Reset selection if out of bounds
		if m.selected >= m.totalVisibleItems() {
			m.selected = max(0, m.totalVisibleItems()-1)
		}
		return m, nil

	case TopicRestoredMsg:
		// Reload topics after restoration
		return m, m.loadTopics()

	case topicsLoadErrorMsg:
		m.err = msg.err
		m.loading = false
		return m, nil

	case topicRestoreErrorMsg:
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	}

	return m, nil
}

// handleKeyPress processes key events for the topics view.
func (m TopicsModel) handleKeyPress(msg tea.KeyMsg) (TopicsModel, tea.Cmd) {
	// If confirmation dialog is visible, let it handle keys
	if m.confirmDialog != nil && m.confirmDialog.IsVisible() {
		confirmed, cancelled, handled := m.confirmDialog.HandleKey(msg.String())
		if handled {
			if confirmed {
				// User confirmed restoration
				topic := m.confirmDialog.GetTopic()
				m.confirmDialog.Hide()
				return m, m.restoreTopic(topic)
			}
			if cancelled {
				m.confirmDialog.Hide()
			}
			return m, nil
		}
	}

	switch msg.String() {
	case "j", "down":
		if m.selected < m.totalVisibleItems()-1 {
			m.selected++
		}
		return m, nil

	case "k", "up":
		if m.selected > 0 {
			m.selected--
		}
		return m, nil

	case "a":
		// Toggle showing archived topics
		m.showArchived = !m.showArchived
		// Reset selection if needed
		if m.selected >= m.totalVisibleItems() {
			m.selected = max(0, m.totalVisibleItems()-1)
		}
		return m, nil

	case "r", "ctrl+r":
		// Show confirmation dialog for restoring selected archived topic
		selectedTopic := m.getSelectedTopic()
		if selectedTopic != nil && selectedTopic.IsArchived() {
			if m.confirmDialog != nil {
				m.confirmDialog.Show(selectedTopic)
			} else {
				// Fallback: restore directly without dialog
				return m, m.restoreTopic(selectedTopic)
			}
		}
		return m, nil

	case "g":
		// Go to first item
		m.selected = 0
		return m, nil

	case "G":
		// Go to last item
		m.selected = max(0, m.totalVisibleItems()-1)
		return m, nil

	case "esc":
		// Hide confirmation dialog if visible
		if m.confirmDialog != nil && m.confirmDialog.IsVisible() {
			m.confirmDialog.Hide()
		}
		return m, nil
	}

	return m, nil
}

// restoreSelectedTopic creates a command to restore the selected archived topic.
// Deprecated: Use restoreTopic with confirmation dialog instead.
func (m TopicsModel) restoreSelectedTopic() tea.Cmd {
	selectedTopic := m.getSelectedTopic()
	if selectedTopic == nil || !selectedTopic.IsArchived() {
		return nil
	}
	return m.restoreTopic(selectedTopic)
}

// restoreTopic creates a command to restore a specific archived topic.
func (m TopicsModel) restoreTopic(topic *storage.Topic) tea.Cmd {
	if topic == nil || !topic.IsArchived() {
		return nil
	}

	return func() tea.Msg {
		if m.archiver == nil {
			return topicRestoreErrorMsg{err: fmt.Errorf("archiver not initialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Restore with placeholder usage values (real impl would calculate these)
		restored, err := m.archiver.RestoreTopic(ctx, topic.ID, 0.5, 0.6)
		if err != nil {
			return topicRestoreErrorMsg{err: err}
		}

		return TopicRestoredMsg{Topic: restored}
	}
}

// totalVisibleItems returns the total number of visible items based on showArchived.
func (m TopicsModel) totalVisibleItems() int {
	count := len(m.topics)
	if m.showArchived {
		count += len(m.archived)
	}
	return count
}

// getSelectedTopic returns the currently selected topic.
func (m TopicsModel) getSelectedTopic() *storage.Topic {
	if m.selected < len(m.topics) {
		return m.topics[m.selected]
	}
	if m.showArchived {
		archivedIdx := m.selected - len(m.topics)
		if archivedIdx < len(m.archived) {
			return m.archived[archivedIdx]
		}
	}
	return nil
}

// View implements tea.Model. It renders the topics view.
func (m TopicsModel) View() string {
	var b strings.Builder

	// Title with counts
	activeCount := len(m.topics)
	archivedCount := len(m.archived)
	title := fmt.Sprintf("Topics (%d active, %d archived)", activeCount, archivedCount)
	b.WriteString(m.styles.PanelTitle.Render(title))
	b.WriteString("\n\n")

	// Show error if present
	if m.err != nil {
		b.WriteString(m.styles.ErrorLabel.Render("ERROR"))
		b.WriteString(" ")
		b.WriteString(m.styles.Error.Render(m.err.Error()))
		b.WriteString("\n\n")
	}

	// Show loading state
	if m.loading {
		b.WriteString(m.styles.Placeholder.Render("Loading topics..."))
		return m.wrapInPanel(b.String())
	}

	// Show empty state
	if len(m.topics) == 0 && len(m.archived) == 0 {
		b.WriteString(m.styles.Placeholder.Render("No topics found for this conversation."))
		b.WriteString("\n\n")
		b.WriteString(m.styles.Help.Render("Topics are created as you chat about different subjects."))
		return m.wrapInPanel(b.String())
	}

	// Show confirmation dialog if visible
	if m.confirmDialog != nil && m.confirmDialog.IsVisible() {
		b.WriteString(m.confirmDialog.View())
		b.WriteString("\n\n")
	}

	// Calculate allocation total for percentage display
	var totalTokens int
	for _, t := range m.topics {
		totalTokens += t.TokenCount
	}

	// Render active topics
	for i, t := range m.topics {
		isSelected := i == m.selected
		b.WriteString(m.renderTopic(t, isSelected, totalTokens))
		b.WriteString("\n")
	}

	// Render archived topics if showing
	if m.showArchived && len(m.archived) > 0 {
		b.WriteString("\n")
		b.WriteString(m.styles.TopicArchived.Render("--- Archived ---"))
		b.WriteString("\n\n")

		for i, t := range m.archived {
			isSelected := i+len(m.topics) == m.selected
			b.WriteString(m.renderTopic(t, isSelected, totalTokens))
			b.WriteString("\n")
		}
	}

	// Help text
	b.WriteString("\n")
	helpItems := []string{
		m.styles.RenderKeyBinding("j/k", "navigate"),
		m.styles.RenderKeyBinding("a", "toggle archived"),
	}
	if m.showArchived && len(m.archived) > 0 {
		helpItems = append(helpItems, m.styles.RenderKeyBinding("r", "restore"))
	}
	b.WriteString(strings.Join(helpItems, "  "))

	return m.wrapInPanel(b.String())
}

// renderTopic renders a single topic entry.
func (m TopicsModel) renderTopic(t *storage.Topic, isSelected bool, totalTokens int) string {
	var b strings.Builder

	// Selection indicator and status
	prefix := "  "
	if isSelected {
		prefix = m.styles.TopicCurrent.Render("> ")
	}

	// Topic name with status badge
	var nameStyle lipgloss.Style
	var statusBadge string

	if t.IsArchived() {
		nameStyle = m.styles.TopicArchived
		statusBadge = m.styles.TopicArchived.Render("[Archived]")
	} else if t.IsCurrent {
		nameStyle = m.styles.TopicCurrent
		statusBadge = m.styles.TopicCurrent.Render("[Current]")
	} else {
		nameStyle = m.styles.TopicActive
		statusBadge = m.styles.TopicActive.Render("[Active]")
	}

	// Current topic star
	star := ""
	if t.IsCurrent {
		star = m.styles.TopicCurrent.Render("\u2605 ") // Unicode star
	}

	// First line: name and status
	name := nameStyle.Render(t.Name)
	b.WriteString(prefix)
	b.WriteString(star)
	b.WriteString(name)
	b.WriteString("  ")
	b.WriteString(statusBadge)
	b.WriteString("\n")

	// Second line: keywords (indented)
	indent := "      "
	if len(t.Keywords) > 0 {
		keywordsStr := strings.Join(t.Keywords, ", ")
		if len(keywordsStr) > 50 {
			keywordsStr = keywordsStr[:47] + "..."
		}
		b.WriteString(indent)
		b.WriteString(m.styles.Help.Render("Keywords: " + keywordsStr))
		b.WriteString("\n")
	}

	// Third line: tokens and relevance
	b.WriteString(indent)

	// Token count and allocation percentage
	tokenStr := formatTokenCount(t.TokenCount)
	if t.IsArchived() {
		// Show archived time instead of allocation
		archivedAgo := formatTimeAgo(t.ArchivedAt)
		b.WriteString(m.styles.Help.Render(fmt.Sprintf("Tokens: %s | Archived: %s", tokenStr, archivedAgo)))
	} else {
		// Calculate allocation percentage
		allocPct := 0.0
		if totalTokens > 0 {
			allocPct = float64(t.TokenCount) / float64(totalTokens) * 100
		}
		b.WriteString(m.styles.Help.Render(fmt.Sprintf("Tokens: %s (%.0f%%)", tokenStr, allocPct)))

		// Relevance score with visual bar
		b.WriteString(m.styles.Help.Render(" | Relevance: "))
		b.WriteString(m.renderRelevanceBar(t.RelevanceScore))
		b.WriteString(m.styles.Help.Render(fmt.Sprintf(" %.2f", t.RelevanceScore)))
	}
	b.WriteString("\n")

	return b.String()
}

// renderRelevanceBar renders a visual bar for relevance score.
func (m TopicsModel) renderRelevanceBar(score float64) string {
	const barWidth = 5
	filled := int(score * barWidth)
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}

	var bar string
	for i := range barWidth {
		if i < filled {
			bar += m.styles.DebugBar.Render("\u2588") // Full block
		} else {
			bar += m.styles.Help.Render("\u2591") // Light shade
		}
	}
	return bar
}

// wrapInPanel wraps content in a styled panel.
func (m TopicsModel) wrapInPanel(content string) string {
	panelWidth := m.width - 6
	if panelWidth < 40 {
		panelWidth = 40
	}

	return m.styles.Panel.
		Width(panelWidth).
		Render(content)
}

// SetSize updates the dimensions of the topics view.
func (m *TopicsModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	if m.confirmDialog != nil {
		m.confirmDialog.SetWidth(width)
	}
}

// SetConversation updates the conversation ID and reloads topics.
func (m *TopicsModel) SetConversation(convID string) tea.Cmd {
	m.convID = convID
	m.loading = true
	return m.loadTopics()
}

// Refresh reloads the topics.
func (m TopicsModel) Refresh() tea.Cmd {
	m.loading = true
	return m.loadTopics()
}

// GetSelectedTopic returns the currently selected topic (exported).
func (m TopicsModel) GetSelectedTopic() *storage.Topic {
	return m.getSelectedTopic()
}

// ShowingArchived returns whether archived topics are being shown.
func (m TopicsModel) ShowingArchived() bool {
	return m.showArchived
}

// ActiveCount returns the number of active topics.
func (m TopicsModel) ActiveCount() int {
	return len(m.topics)
}

// ArchivedCount returns the number of archived topics.
func (m TopicsModel) ArchivedCount() int {
	return len(m.archived)
}

// --- Helper functions ---

// formatTokenCount formats a token count with thousands separators.
func formatTokenCount(count int) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 1000000 {
		return fmt.Sprintf("%d,%03d", count/1000, count%1000)
	}
	return fmt.Sprintf("%d,%03d,%03d", count/1000000, (count/1000)%1000, count%1000)
}

// formatTimeAgo formats a time as a human-readable "ago" string.
func formatTimeAgo(t *time.Time) string {
	if t == nil {
		return "unknown"
	}

	duration := time.Since(*t)

	if duration < time.Minute {
		return "just now"
	}
	if duration < time.Hour {
		mins := int(duration.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", mins)
	}
	if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", hours)
	}

	days := int(duration.Hours() / 24)
	if days == 1 {
		return "1d ago"
	}
	return fmt.Sprintf("%dd ago", days)
}
