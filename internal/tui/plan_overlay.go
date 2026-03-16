package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/hegner123/nostop/internal/plan"
	"github.com/hegner123/nostop/pkg/nostop"
)

// PlanLoadedMsg is sent when a plan has been loaded.
type PlanLoadedMsg struct {
	Plan *plan.Plan
}

// PlanRefreshedMsg is sent when a plan has been refreshed.
type PlanRefreshedMsg struct {
	ChangedIDs []string
}

// WorkUnitSelectedMsg is sent when a work unit is selected as active.
type WorkUnitSelectedMsg struct {
	WorkUnitID string
	Name       string
}

// WorkUnitCompletedMsg is sent when a work unit is marked complete.
type WorkUnitCompletedMsg struct {
	WorkUnitID string
}

// WorkUnitArchivedMsg is sent when a work unit is archived (Phase D).
type WorkUnitArchivedMsg struct {
	WorkUnitID string
}

// WorkUnitRestoredMsg is sent when a work unit is restored (Phase D).
type WorkUnitRestoredMsg struct {
	WorkUnitID string
}

// PlanInitResultMsg is sent when /plan init completes structure detection.
type PlanInitResultMsg struct {
	Detection *plan.DetectionResult
	SchemaPath string // path where schema was saved, empty if ambiguous
}

// planLoadErrorMsg is sent when loading a plan fails.
type planLoadErrorMsg struct {
	err error
}

// planRefreshErrorMsg is sent when refreshing a plan fails.
type planRefreshErrorMsg struct {
	err error
}

// PlanOverlay shows the plan tree for work unit selection.
type PlanOverlay struct {
	ctx      context.Context
	tracker  *nostop.PlanTracker
	engine   *nostop.Nostop
	archiver *nostop.Archiver
	convID   string
	width    int
	height   int
	err      error
	styles   Styles
	loading  bool

	// Tree navigation
	expanded    map[string]bool  // Tracks which units are expanded
	selected    int              // Index in visible list
	visibleList []*plan.WorkUnit // Flattened visible list

	// Work unit stats cache (Phase D)
	statsCache map[string]*nostop.WorkUnitStats

	// Confirmation dialogs
	confirmComplete bool
	confirmArchive  bool // Phase D: archive confirmation
	confirmRestore  bool // Phase D: restore confirmation
	confirmTarget   *plan.WorkUnit

	// Summary preview mode (Phase D)
	showSummary    bool
	summaryContent string
}

// NewPlanOverlay creates a new PlanOverlay.
func NewPlanOverlay(tracker *nostop.PlanTracker, engine *nostop.Nostop, convID string, ctx context.Context, width, height int) *PlanOverlay {
	return &PlanOverlay{
		ctx:        ctx,
		tracker:    tracker,
		engine:     engine,
		convID:     convID,
		width:      width,
		height:     height,
		styles:     DefaultStyles(),
		expanded:   make(map[string]bool),
		statsCache: make(map[string]*nostop.WorkUnitStats),
	}
}

// SetArchiver sets the archiver for work unit archival operations.
func (m *PlanOverlay) SetArchiver(archiver *nostop.Archiver) {
	m.archiver = archiver
}

// Init implements tea.Model.
func (m PlanOverlay) Init() tea.Cmd {
	return m.buildVisibleList
}

// buildVisibleList creates the flattened list of visible work units.
func (m *PlanOverlay) buildVisibleList() tea.Msg {
	if m.tracker == nil || !m.tracker.HasPlan() {
		m.visibleList = nil
		return nil
	}

	var list []*plan.WorkUnit
	roots := m.tracker.GetRootUnits()

	var walk func(units []*plan.WorkUnit, depth int)
	walk = func(units []*plan.WorkUnit, depth int) {
		for _, wu := range units {
			list = append(list, wu)
			if m.expanded[wu.ID] {
				children := m.tracker.GetChildren(wu.ID)
				walk(children, depth+1)
			}
		}
	}

	walk(roots, 0)
	m.visibleList = list
	return nil
}

// Update implements tea.Model.
func (m PlanOverlay) Update(msg tea.Msg) (PlanOverlay, tea.Cmd) {
	switch msg := msg.(type) {
	case PlanLoadedMsg:
		m.loading = false
		m.err = nil
		m.statsCache = make(map[string]*nostop.WorkUnitStats) // Clear cache
		// Expand root units by default
		if m.tracker != nil {
			for _, id := range m.tracker.GetPlan().Root {
				m.expanded[id] = true
			}
		}
		m.buildVisibleList()
		return m, nil

	case PlanRefreshedMsg:
		m.statsCache = make(map[string]*nostop.WorkUnitStats) // Clear cache on refresh
		m.buildVisibleList()
		return m, nil

	case planLoadErrorMsg:
		m.err = msg.err
		m.loading = false
		return m, nil

	case planRefreshErrorMsg:
		m.err = msg.err
		return m, nil

	case WorkUnitArchivedMsg:
		// Clear cache and rebuild list after archive
		m.statsCache = make(map[string]*nostop.WorkUnitStats)
		m.buildVisibleList()
		return m, nil

	case WorkUnitRestoredMsg:
		// Clear cache and rebuild list after restore
		m.statsCache = make(map[string]*nostop.WorkUnitStats)
		m.buildVisibleList()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	}

	return m, nil
}

// handleKeyPress processes key events.
func (m PlanOverlay) handleKeyPress(msg tea.KeyPressMsg) (PlanOverlay, tea.Cmd) {
	// Summary preview mode - any key dismisses it
	if m.showSummary {
		m.showSummary = false
		m.summaryContent = ""
		return m, nil
	}

	// Confirmation modes
	if m.confirmComplete {
		switch msg.String() {
		case "y", "Y":
			target := m.confirmTarget
			m.confirmComplete = false
			m.confirmTarget = nil
			return m, m.completeWorkUnit(target)
		case "n", "N", "esc":
			m.confirmComplete = false
			m.confirmTarget = nil
			return m, nil
		}
		return m, nil
	}

	if m.confirmArchive {
		switch msg.String() {
		case "y", "Y":
			target := m.confirmTarget
			m.confirmArchive = false
			m.confirmTarget = nil
			return m, m.archiveWorkUnit(target)
		case "n", "N", "esc":
			m.confirmArchive = false
			m.confirmTarget = nil
			return m, nil
		}
		return m, nil
	}

	if m.confirmRestore {
		switch msg.String() {
		case "y", "Y":
			target := m.confirmTarget
			m.confirmRestore = false
			m.confirmTarget = nil
			return m, m.restoreWorkUnit(target)
		case "n", "N", "esc":
			m.confirmRestore = false
			m.confirmTarget = nil
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "j", "down":
		if m.selected < len(m.visibleList)-1 {
			m.selected++
		}
		return m, nil

	case "k", "up":
		if m.selected > 0 {
			m.selected--
		}
		return m, nil

	case "g":
		m.selected = 0
		return m, nil

	case "G":
		m.selected = max(0, len(m.visibleList)-1)
		return m, nil

	case "l", "right", "tab":
		// Expand selected unit
		if wu := m.getSelectedUnit(); wu != nil && len(wu.Children) > 0 {
			m.expanded[wu.ID] = true
			m.buildVisibleList()
		}
		return m, nil

	case "h", "left", "shift+tab":
		// Collapse selected unit
		if wu := m.getSelectedUnit(); wu != nil {
			if m.expanded[wu.ID] {
				m.expanded[wu.ID] = false
				m.buildVisibleList()
			} else if wu.Parent != "" {
				// Navigate to parent
				for i, u := range m.visibleList {
					if u.ID == wu.Parent {
						m.selected = i
						break
					}
				}
			}
		}
		return m, nil

	case "enter":
		// Select as active work unit
		if wu := m.getSelectedUnit(); wu != nil {
			return m, m.selectWorkUnit(wu)
		}
		return m, nil

	case "x":
		// Mark as complete
		if wu := m.getSelectedUnit(); wu != nil && wu.Status != plan.UnitComplete && wu.Status != plan.UnitArchived {
			m.confirmComplete = true
			m.confirmTarget = wu
		}
		return m, nil

	case "a":
		// Phase D: Archive completed work unit
		if wu := m.getSelectedUnit(); wu != nil && wu.Status == plan.UnitComplete {
			m.confirmArchive = true
			m.confirmTarget = wu
		}
		return m, nil

	case "u":
		// Phase D: Restore (unarchive) archived work unit
		if wu := m.getSelectedUnit(); wu != nil && wu.Status == plan.UnitArchived {
			m.confirmRestore = true
			m.confirmTarget = wu
		}
		return m, nil

	case "s":
		// Phase D: Show summary preview for archived units
		if wu := m.getSelectedUnit(); wu != nil && wu.Status == plan.UnitArchived {
			return m, m.loadSummaryPreview(wu)
		}
		return m, nil

	case "r":
		// Refresh plan
		return m, m.refreshPlan()

	case "e":
		// Expand all
		for _, wu := range m.tracker.GetWorkUnits() {
			if len(wu.Children) > 0 {
				m.expanded[wu.ID] = true
			}
		}
		m.buildVisibleList()
		return m, nil

	case "c":
		// Collapse all
		m.expanded = make(map[string]bool)
		m.buildVisibleList()
		return m, nil
	}

	return m, nil
}

// getSelectedUnit returns the currently selected work unit.
func (m *PlanOverlay) getSelectedUnit() *plan.WorkUnit {
	if m.selected < 0 || m.selected >= len(m.visibleList) {
		return nil
	}
	return m.visibleList[m.selected]
}

// selectWorkUnit creates a command to select a work unit as active.
func (m *PlanOverlay) selectWorkUnit(wu *plan.WorkUnit) tea.Cmd {
	return func() tea.Msg {
		if m.tracker == nil {
			return planLoadErrorMsg{err: fmt.Errorf("no plan tracker")}
		}

		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()

		if err := m.tracker.SetActiveWorkUnit(ctx, wu.ID); err != nil {
			return planLoadErrorMsg{err: err}
		}

		return WorkUnitSelectedMsg{
			WorkUnitID: wu.ID,
			Name:       wu.Name,
		}
	}
}

// completeWorkUnit creates a command to mark a work unit complete.
func (m *PlanOverlay) completeWorkUnit(wu *plan.WorkUnit) tea.Cmd {
	return func() tea.Msg {
		if m.tracker == nil {
			return planLoadErrorMsg{err: fmt.Errorf("no plan tracker")}
		}

		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()

		if err := m.tracker.CompleteWorkUnit(ctx, wu.ID); err != nil {
			return planLoadErrorMsg{err: err}
		}

		return WorkUnitCompletedMsg{
			WorkUnitID: wu.ID,
		}
	}
}

// archiveWorkUnit creates a command to archive a completed work unit (Phase D).
func (m *PlanOverlay) archiveWorkUnit(wu *plan.WorkUnit) tea.Cmd {
	return func() tea.Msg {
		if m.archiver == nil {
			return planLoadErrorMsg{err: fmt.Errorf("no archiver configured")}
		}

		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()

		// Get current usage for logging (simplified - real impl would calc from context)
		usageBefore := 0.5
		usageAfter := 0.45

		if err := m.archiver.ArchiveWorkUnit(ctx, wu.ID, wu.Name, usageBefore, usageAfter); err != nil {
			return planLoadErrorMsg{err: err}
		}

		// Update in-memory plan status
		if m.tracker != nil && m.tracker.HasPlan() {
			if planWU := m.tracker.GetPlan().GetUnit(wu.ID); planWU != nil {
				planWU.Status = plan.UnitArchived
			}
		}

		return WorkUnitArchivedMsg{
			WorkUnitID: wu.ID,
		}
	}
}

// restoreWorkUnit creates a command to restore an archived work unit (Phase D).
func (m *PlanOverlay) restoreWorkUnit(wu *plan.WorkUnit) tea.Cmd {
	return func() tea.Msg {
		if m.archiver == nil {
			return planLoadErrorMsg{err: fmt.Errorf("no archiver configured")}
		}

		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()

		// Get current usage for logging
		usageBefore := 0.45
		usageAfter := 0.5

		// keepSummary = false to remove it on restore
		if err := m.archiver.RestoreWorkUnit(ctx, wu.ID, false, usageBefore, usageAfter); err != nil {
			return planLoadErrorMsg{err: err}
		}

		// Update in-memory plan status
		if m.tracker != nil && m.tracker.HasPlan() {
			if planWU := m.tracker.GetPlan().GetUnit(wu.ID); planWU != nil {
				planWU.Status = plan.UnitComplete
			}
		}

		return WorkUnitRestoredMsg{
			WorkUnitID: wu.ID,
		}
	}
}

// loadSummaryPreview fetches and displays the summary for an archived work unit.
func (m *PlanOverlay) loadSummaryPreview(wu *plan.WorkUnit) tea.Cmd {
	return func() tea.Msg {
		if m.archiver == nil {
			return planLoadErrorMsg{err: fmt.Errorf("no archiver configured")}
		}

		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()

		stats, err := m.archiver.GetWorkUnitStats(ctx, wu.ID)
		if err != nil {
			return planLoadErrorMsg{err: err}
		}

		if stats.HasSummary {
			// Fetch summary content via storage
			// For now, set a placeholder - real impl would query DB
			m.showSummary = true
			m.summaryContent = fmt.Sprintf("Summary for: %s\nTokens: %d (archived)\nMessages: %d",
				wu.Name, stats.ArchivedTokens, stats.MessageCount)
		}

		return nil
	}
}

// refreshPlan creates a command to refresh the plan from file.
func (m *PlanOverlay) refreshPlan() tea.Cmd {
	return func() tea.Msg {
		if m.tracker == nil {
			return planRefreshErrorMsg{err: fmt.Errorf("no plan tracker")}
		}

		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()

		changedIDs, err := m.tracker.RefreshPlan(ctx)
		if err != nil {
			return planRefreshErrorMsg{err: err}
		}

		return PlanRefreshedMsg{ChangedIDs: changedIDs}
	}
}

// View implements tea.Model.
func (m PlanOverlay) View() string {
	var b strings.Builder

	// Title
	title := "Plan: "
	if m.tracker != nil && m.tracker.HasPlan() {
		title += m.tracker.GetPlanFile()
	} else {
		title += "(none loaded)"
	}
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
		b.WriteString(m.styles.Placeholder.Render("Loading plan..."))
		return m.wrapInPanel(b.String())
	}

	// Show empty state
	if m.tracker == nil || !m.tracker.HasPlan() {
		b.WriteString(m.styles.Placeholder.Render("No plan loaded."))
		b.WriteString("\n\n")
		b.WriteString(m.styles.Help.Render("Load a plan with the /plan command."))
		return m.wrapInPanel(b.String())
	}

	// Show summary preview (Phase D)
	if m.showSummary && m.summaryContent != "" {
		b.WriteString(m.renderSummaryPreview())
		b.WriteString("\n\n")
		b.WriteString(m.styles.Help.Render("Press any key to dismiss"))
		return m.wrapInPanel(b.String())
	}

	// Show confirmation dialogs
	if m.confirmComplete && m.confirmTarget != nil {
		b.WriteString(m.renderConfirmDialog("Complete Work Unit", m.confirmTarget.Name, "Mark as complete?"))
		b.WriteString("\n\n")
	}
	if m.confirmArchive && m.confirmTarget != nil {
		b.WriteString(m.renderConfirmDialog("Archive Work Unit", m.confirmTarget.Name, "Archive and create summary?"))
		b.WriteString("\n\n")
	}
	if m.confirmRestore && m.confirmTarget != nil {
		b.WriteString(m.renderConfirmDialog("Restore Work Unit", m.confirmTarget.Name, "Restore archived messages?"))
		b.WriteString("\n\n")
	}

	// Show active work unit
	if activeID := m.tracker.GetActiveWorkUnitID(); activeID != "" {
		if wu := m.tracker.GetPlan().GetUnit(activeID); wu != nil {
			activeStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Bold(true)
			b.WriteString(activeStyle.Render("Active: "))
			b.WriteString(m.styles.TopicActive.Render(wu.Name))
			b.WriteString("\n\n")
		}
	}

	// Show stats
	if stats := m.tracker.GetStats(); stats != nil {
		b.WriteString(m.styles.Help.Render(fmt.Sprintf(
			"%d pending · %d active · %d complete · %d archived",
			stats.PendingUnits, stats.ActiveUnits, stats.CompleteUnits, stats.ArchivedUnits,
		)))
		b.WriteString("\n\n")
	}

	// Render tree
	for i, wu := range m.visibleList {
		isSelected := i == m.selected
		b.WriteString(m.renderWorkUnit(wu, isSelected))
		b.WriteString("\n")
	}

	// Help text
	b.WriteString("\n")
	helpItems := []string{
		m.styles.RenderKeyBinding("j/k", "navigate"),
		m.styles.RenderKeyBinding("l/h", "expand/collapse"),
		m.styles.RenderKeyBinding("enter", "select"),
		m.styles.RenderKeyBinding("x", "complete"),
		m.styles.RenderKeyBinding("a", "archive"),
		m.styles.RenderKeyBinding("u", "restore"),
		m.styles.RenderKeyBinding("s", "summary"),
		m.styles.RenderKeyBinding("r", "refresh"),
	}
	b.WriteString(strings.Join(helpItems, "  "))

	return m.wrapInPanel(b.String())
}

// renderWorkUnit renders a single work unit entry.
func (m *PlanOverlay) renderWorkUnit(wu *plan.WorkUnit, isSelected bool) string {
	var b strings.Builder

	// Calculate indent based on depth
	depth := wu.Depth()
	indent := strings.Repeat("  ", depth)

	// Selection indicator
	prefix := "  "
	if isSelected {
		prefix = m.styles.TopicCurrent.Render("> ")
	}

	// Expand/collapse indicator
	expandIndicator := "  "
	if len(wu.Children) > 0 {
		if m.expanded[wu.ID] {
			expandIndicator = "▼ "
		} else {
			expandIndicator = "▶ "
		}
	}

	// Status indicator
	var statusIndicator string
	var nameStyle lipgloss.Style
	switch wu.Status {
	case plan.UnitPending:
		statusIndicator = "○"
		nameStyle = m.styles.Placeholder
	case plan.UnitActive:
		statusIndicator = "●"
		nameStyle = m.styles.TopicActive
	case plan.UnitComplete:
		statusIndicator = "✓"
		nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Strikethrough(true)
	case plan.UnitArchived:
		statusIndicator = "▣"
		nameStyle = m.styles.TopicArchived
	}

	// Check if this is the active unit
	isActive := m.tracker.GetActiveWorkUnitID() == wu.ID
	if isActive {
		statusIndicator = "★"
		nameStyle = m.styles.TopicCurrent
	}

	// Build line
	b.WriteString(prefix)
	b.WriteString(indent)
	b.WriteString(expandIndicator)
	b.WriteString(m.styles.Help.Render(statusIndicator))
	b.WriteString(" ")

	// Truncate name if needed
	maxNameLen := m.width - len(indent) - 20 // Reserve space for tokens
	name := wu.Name
	if len(name) > maxNameLen && maxNameLen > 3 {
		name = name[:maxNameLen-3] + "..."
	}
	b.WriteString(nameStyle.Render(name))

	// Level badge for non-leaf units
	if !wu.IsLeaf() {
		b.WriteString(" ")
		levelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true)
		b.WriteString(levelStyle.Render(fmt.Sprintf("[%s]", wu.Level)))
	}

	// Token count display (Phase D) - show for selected or archived units
	if isSelected || wu.Status == plan.UnitArchived || wu.Status == plan.UnitComplete {
		if stats := m.getWorkUnitStats(wu.ID); stats != nil && stats.TotalTokens > 0 {
			tokenStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("243")).
				Italic(true)
			tokenStr := formatTokensCompact(stats.TotalTokens)
			if stats.ArchivedTokens > 0 {
				tokenStr = fmt.Sprintf("%s archived", formatTokensCompact(stats.ArchivedTokens))
			}
			b.WriteString(" ")
			b.WriteString(tokenStyle.Render(fmt.Sprintf("(%s)", tokenStr)))
		}
	}

	return b.String()
}

// getWorkUnitStats retrieves stats with caching.
func (m *PlanOverlay) getWorkUnitStats(workUnitID string) *nostop.WorkUnitStats {
	if m.archiver == nil {
		return nil
	}

	// Check cache
	if stats, ok := m.statsCache[workUnitID]; ok {
		return stats
	}

	// Fetch from archiver
	ctx, cancel := context.WithTimeout(m.ctx, 1*time.Second)
	defer cancel()

	stats, err := m.archiver.GetWorkUnitStats(ctx, workUnitID)
	if err != nil {
		return nil
	}

	m.statsCache[workUnitID] = stats
	return stats
}

// formatTokensCompact formats token count compactly.
func formatTokensCompact(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fK", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// renderConfirmDialog renders a confirmation dialog.
func (m *PlanOverlay) renderConfirmDialog(title, name, prompt string) string {
	confirmStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("42")).
		Padding(1, 2).
		Width(m.width - 16)

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))

	var b strings.Builder
	b.WriteString(headerStyle.Render(title))
	b.WriteString("\n\n")
	b.WriteString(m.styles.Info.Render("Name: "))
	b.WriteString(m.styles.TopicActive.Render(name))
	b.WriteString("\n\n")
	b.WriteString(prompt + " ")
	b.WriteString(keyStyle.Render("[y]"))
	b.WriteString(m.styles.Help.Render(" yes  "))
	b.WriteString(keyStyle.Render("[n/esc]"))
	b.WriteString(m.styles.Help.Render(" cancel"))

	return confirmStyle.Render(b.String())
}

// renderSummaryPreview renders the summary preview panel (Phase D).
func (m *PlanOverlay) renderSummaryPreview() string {
	previewStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("39")).
		Padding(1, 2).
		Width(m.width - 10)

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))

	var b strings.Builder
	b.WriteString(headerStyle.Render("Archived Summary"))
	b.WriteString("\n\n")
	b.WriteString(m.summaryContent)

	return previewStyle.Render(b.String())
}

// wrapInPanel wraps content in a styled panel.
func (m *PlanOverlay) wrapInPanel(content string) string {
	panelWidth := m.width - 6
	if panelWidth < 40 {
		panelWidth = 40
	}

	return m.styles.Panel.
		Width(panelWidth).
		Render(content)
}

// OverlayUpdate implements ModalOverlay.
func (m *PlanOverlay) OverlayUpdate(msg tea.KeyMsg) (ModalOverlay, tea.Cmd) {
	if msg.String() == "esc" && !m.confirmComplete && !m.confirmArchive && !m.confirmRestore && !m.showSummary {
		return nil, nil
	}
	updated, cmd := (*m).Update(msg)
	*m = updated
	return m, cmd
}

// OverlayView implements ModalOverlay.
func (m *PlanOverlay) OverlayView(width, height int) string {
	dlgW := width * 3 / 4
	dlgH := height * 3 / 4
	if dlgW < 60 {
		dlgW = 60
	}
	if dlgH < 20 {
		dlgH = 20
	}

	contentW := dlgW - 6
	contentH := dlgH - 4
	m.SetSize(contentW, contentH)

	style := overlayStyle().Width(dlgW).Height(dlgH)
	return style.Render(m.View())
}

// SetSize updates the dimensions.
func (m *PlanOverlay) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// SetTracker sets the plan tracker.
func (m *PlanOverlay) SetTracker(tracker *nostop.PlanTracker) {
	m.tracker = tracker
	m.buildVisibleList()
}

// Refresh reloads the visible list.
func (m *PlanOverlay) Refresh() tea.Cmd {
	m.buildVisibleList()
	return nil
}
