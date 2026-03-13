package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hegner123/nostop/internal/topic"
	"github.com/hegner123/nostop/pkg/nostop"
)

// ArchiveEvent represents a historical archive/restore event.
type ArchiveEvent struct {
	Timestamp time.Time
	TopicName string
	Tokens    int
	IsRestore bool // true = restore, false = archive
}

// DebugModel is the model for the debug/context info view.
type DebugModel struct {
	contextMgr  *nostop.ContextManager
	tracker     *topic.TopicTracker
	convID      string
	usage       *nostop.ContextUsage
	width       int
	height      int
	autoRefresh bool
	err         error
	events      []ArchiveEvent // Archive event history
	styles      Styles
}

// Custom messages for the debug view.

// ContextUsageMsg carries updated context usage information.
type ContextUsageMsg struct {
	Usage *nostop.ContextUsage
}

// RefreshDebugMsg triggers a refresh of debug information.
type RefreshDebugMsg struct{}

// TickMsg is sent periodically for auto-refresh.
type TickMsg struct{}

// DebugErrorMsg carries an error from async operations.
type DebugErrorMsg struct {
	Err error
}

// NewDebugModel creates a new DebugModel instance.
func NewDebugModel(contextMgr *nostop.ContextManager, tracker *topic.TopicTracker, convID string, width, height int) *DebugModel {
	return &DebugModel{
		contextMgr:  contextMgr,
		tracker:     tracker,
		convID:      convID,
		width:       width,
		height:      height,
		autoRefresh: true,
		events:      make([]ArchiveEvent, 0),
		styles:      DefaultStyles(),
	}
}

// Init implements tea.Model. It returns the initial command to load context info.
func (d DebugModel) Init() tea.Cmd {
	return d.refreshCmd()
}

// Update implements tea.Model. It handles messages and updates the model.
func (d DebugModel) Update(msg tea.Msg) (DebugModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			// Toggle auto-refresh
			d.autoRefresh = !d.autoRefresh
			if d.autoRefresh {
				return d, tea.Batch(d.refreshCmd(), d.tickCmd())
			}
			return d, nil
		case "R":
			// Manual refresh
			return d, d.refreshCmd()
		}

	case ContextUsageMsg:
		d.usage = msg.Usage
		d.err = nil
		return d, nil

	case RefreshDebugMsg:
		return d, d.refreshCmd()

	case TickMsg:
		if d.autoRefresh {
			return d, tea.Batch(d.refreshCmd(), d.tickCmd())
		}
		return d, nil

	case DebugErrorMsg:
		d.err = msg.Err
		return d, nil

	case tea.WindowSizeMsg:
		d.width = msg.Width
		d.height = msg.Height
		return d, nil
	}

	return d, nil
}

// View implements tea.Model. It renders the debug information display.
func (d DebugModel) View() string {
	var b strings.Builder

	// Title
	b.WriteString(d.styles.PanelTitle.Render("Context Debug"))
	b.WriteString("\n\n")

	// Context usage section
	b.WriteString(d.renderContextUsage())
	b.WriteString("\n\n")

	// Topic allocations section
	b.WriteString(d.renderTopicAllocations())
	b.WriteString("\n\n")

	// Thresholds section
	b.WriteString(d.renderThresholds())
	b.WriteString("\n\n")

	// Archive events section
	b.WriteString(d.renderArchiveEvents())
	b.WriteString("\n\n")

	// Auto-refresh status
	autoStatus := "OFF"
	if d.autoRefresh {
		autoStatus = "ON"
	}
	b.WriteString(d.styles.Info.Render(fmt.Sprintf("Auto-refresh: %s (r to toggle, R to refresh)", autoStatus)))

	// Error display if present
	if d.err != nil {
		b.WriteString("\n\n")
		b.WriteString(d.styles.ErrorLabel.Render("ERROR"))
		b.WriteString(" ")
		b.WriteString(d.styles.Error.Render(d.err.Error()))
	}

	return b.String()
}

// renderContextUsage renders the context usage overview with progress bar.
func (d DebugModel) renderContextUsage() string {
	var b strings.Builder

	maxTokens := 200000 // default
	totalTokens := 0
	usagePercent := 0.0
	zone := nostop.ZoneNormal

	if d.contextMgr != nil {
		maxTokens = d.contextMgr.MaxTokens()
	}

	if d.usage != nil {
		totalTokens = d.usage.TotalTokens
		usagePercent = d.usage.UsagePercent
		zone = d.usage.Zone
	}

	// Usage line
	usageStr := fmt.Sprintf("Context Usage: %s / %s tokens",
		formatNumber(totalTokens),
		formatNumber(maxTokens))
	b.WriteString(d.styles.DebugLabel.Render(usageStr))
	b.WriteString("\n")

	// Progress bar
	barWidth := 30
	if d.width > 60 {
		barWidth = 40
	}
	b.WriteString(d.styles.RenderProgressBar(usagePercent, barWidth))
	b.WriteString(fmt.Sprintf(" %.1f%%", usagePercent*100))
	b.WriteString("\n")

	// Zone indicator with color coding
	zoneStyle := d.getZoneStyle(zone)
	b.WriteString("Zone: ")
	b.WriteString(zoneStyle.Render(strings.ToUpper(zone.String())))

	return b.String()
}

// renderTopicAllocations renders the topic allocation breakdown.
func (d DebugModel) renderTopicAllocations() string {
	var b strings.Builder

	// Section header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("240")).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("240"))

	b.WriteString(headerStyle.Render("Topic Allocations"))
	b.WriteString("\n")

	if d.usage == nil || len(d.usage.TopicBreakdown) == 0 {
		b.WriteString(d.styles.Placeholder.Render("No topics yet"))
		return b.String()
	}

	// Sort topics by allocation (highest first)
	type topicInfo struct {
		name       string
		allocation float64
		tokens     int
		isCurrent  bool
		isArchived bool
	}

	topics := make([]topicInfo, 0, len(d.usage.TopicBreakdown))
	for _, t := range d.usage.TopicBreakdown {
		topics = append(topics, topicInfo{
			name:       t.TopicName,
			allocation: t.Allocation,
			tokens:     t.TokenCount,
			isCurrent:  t.IsCurrent,
			isArchived: t.IsArchived,
		})
	}

	sort.Slice(topics, func(i, j int) bool {
		// Current topic first, then by allocation
		if topics[i].isCurrent != topics[j].isCurrent {
			return topics[i].isCurrent
		}
		return topics[i].allocation > topics[j].allocation
	})

	// Render each topic
	for _, t := range topics {
		if t.isArchived {
			continue // Don't show archived topics in allocation view
		}

		// Topic name (truncated if needed)
		name := truncateString(t.name, 20)
		if t.isCurrent {
			name = d.styles.TopicCurrent.Render(name)
		} else {
			name = d.styles.TopicActive.Render(name)
		}

		// Allocation percentage
		allocStr := fmt.Sprintf("%3.0f%%", t.allocation*100)

		// Mini progress bar for allocation
		miniBar := d.renderMiniBar(t.allocation, 12)

		// Token count
		tokenStr := formatNumber(t.tokens)

		line := fmt.Sprintf("%-22s %s %s %6s", name, allocStr, miniBar, tokenStr)
		b.WriteString(line)
		b.WriteString("\n")
	}

	// System reserved
	b.WriteString(fmt.Sprintf("%-22s %s %s %6s",
		d.styles.Placeholder.Render("System Reserved"),
		" 10%",
		d.renderMiniBar(0.10, 12),
		"-"))

	return b.String()
}

// renderThresholds renders the threshold information.
func (d DebugModel) renderThresholds() string {
	var b strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("240")).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("240"))

	b.WriteString(headerStyle.Render("Thresholds"))
	b.WriteString("\n")

	maxTokens := 200000
	if d.contextMgr != nil {
		maxTokens = d.contextMgr.MaxTokens()
	}

	archiveTrigger := int(float64(maxTokens) * nostop.ThresholdArchive)
	archiveTarget := int(float64(maxTokens) * nostop.ArchiveTarget)

	totalTokens := 0
	if d.usage != nil {
		totalTokens = d.usage.TotalTokens
	}

	tokensUntilArchive := archiveTrigger - totalTokens
	if tokensUntilArchive < 0 {
		tokensUntilArchive = 0
	}

	b.WriteString(fmt.Sprintf("Archive Trigger: 95%% (%s tokens)\n",
		formatNumber(archiveTrigger)))
	b.WriteString(fmt.Sprintf("Archive Target:  50%% (%s tokens)\n",
		formatNumber(archiveTarget)))

	if tokensUntilArchive > 0 {
		b.WriteString(d.styles.Success.Render(fmt.Sprintf("Tokens until archive: %s",
			formatNumber(tokensUntilArchive))))
	} else {
		b.WriteString(d.styles.Error.Render("ARCHIVE TRIGGERED"))
	}

	return b.String()
}

// renderArchiveEvents renders the archive event history.
func (d DebugModel) renderArchiveEvents() string {
	var b strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("240")).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("240"))

	b.WriteString(headerStyle.Render("Recent Archive Events"))
	b.WriteString("\n")

	if len(d.events) == 0 {
		b.WriteString(d.styles.Placeholder.Render("No archive events yet"))
		return b.String()
	}

	// Show last 5 events
	start := 0
	if len(d.events) > 5 {
		start = len(d.events) - 5
	}

	for i := len(d.events) - 1; i >= start; i-- {
		event := d.events[i]
		timeStr := event.Timestamp.Format("15:04")
		action := "Archived"
		style := d.styles.Warning
		if event.IsRestore {
			action = "Restored"
			style = d.styles.Success
		}

		line := fmt.Sprintf("[%s] %s \"%s\" (%s)",
			timeStr,
			action,
			truncateString(event.TopicName, 20),
			formatNumber(event.Tokens))
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	return b.String()
}

// renderMiniBar renders a small progress bar.
func (d DebugModel) renderMiniBar(percent float64, width int) string {
	filled := int(float64(width) * percent)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	var bar strings.Builder
	for i := range width {
		if i < filled {
			bar.WriteString(d.styles.DebugBar.Render("\u2588")) // Full block
		} else {
			bar.WriteString(d.styles.Help.Render("\u2591")) // Light shade
		}
	}

	return bar.String()
}

// getZoneStyle returns the appropriate style for a context zone.
func (d DebugModel) getZoneStyle(zone nostop.ContextZone) lipgloss.Style {
	switch zone {
	case nostop.ZoneNormal:
		return d.styles.Success
	case nostop.ZoneMonitor:
		return d.styles.Info
	case nostop.ZoneWarning:
		return d.styles.Warning
	case nostop.ZoneArchive:
		return d.styles.Error
	default:
		return d.styles.StatusText
	}
}

// refreshCmd returns a command to refresh the context usage.
func (d DebugModel) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		if d.contextMgr == nil {
			return ContextUsageMsg{Usage: nil}
		}

		// Reload topics from the database so the tracker has fresh state.
		// Without this, GetUsage reads stale in-memory data.
		if d.tracker != nil && d.convID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = d.tracker.LoadTopics(ctx, d.convID)
		}

		// Get usage (reads from the now-refreshed tracker)
		usage, err := d.contextMgr.GetUsage(nil, nil)
		if err != nil {
			return DebugErrorMsg{Err: err}
		}

		return ContextUsageMsg{Usage: usage}
	}
}

// tickCmd returns a command that sends a TickMsg after a delay.
func (d DebugModel) tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return TickMsg{}
	})
}

// SetSize updates the dimensions for the debug view.
func (d *DebugModel) SetSize(width, height int) {
	d.width = width
	d.height = height
}

// SetUsage sets the context usage directly.
func (d *DebugModel) SetUsage(usage *nostop.ContextUsage) {
	d.usage = usage
}

// AddArchiveEvent adds an archive event to the history.
func (d *DebugModel) AddArchiveEvent(topicName string, tokens int, isRestore bool) {
	event := ArchiveEvent{
		Timestamp: time.Now(),
		TopicName: topicName,
		Tokens:    tokens,
		IsRestore: isRestore,
	}
	d.events = append(d.events, event)

	// Keep only last 20 events
	if len(d.events) > 20 {
		d.events = d.events[len(d.events)-20:]
	}
}

// SetConversation updates the conversation ID.
func (d *DebugModel) SetConversation(convID string) {
	d.convID = convID
}

// SetAutoRefresh sets the auto-refresh state.
func (d *DebugModel) SetAutoRefresh(enabled bool) {
	d.autoRefresh = enabled
}

// IsAutoRefresh returns whether auto-refresh is enabled.
func (d *DebugModel) IsAutoRefresh() bool {
	return d.autoRefresh
}

// Helper functions

// formatNumber formats a number with comma separators.
func formatNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}

	str := fmt.Sprintf("%d", n)
	var result strings.Builder

	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result.WriteRune(',')
		}
		result.WriteRune(c)
	}

	return result.String()
}

// truncateString truncates a string to max length with ellipsis.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
