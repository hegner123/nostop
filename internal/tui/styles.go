// Package tui provides the Bubbletea-based terminal user interface for RLM.
package tui

import "github.com/charmbracelet/lipgloss"

// Color palette - using ANSI colors for broad terminal compatibility.
var (
	// Primary colors
	colorPrimary   = lipgloss.Color("62")  // Purple/blue
	colorSecondary = lipgloss.Color("39")  // Cyan
	colorAccent    = lipgloss.Color("212") // Pink

	// Semantic colors
	colorSuccess = lipgloss.Color("82")  // Green
	colorWarning = lipgloss.Color("214") // Orange
	colorError   = lipgloss.Color("196") // Red
	colorMuted   = lipgloss.Color("240") // Gray

	// Message colors
	colorUser      = lipgloss.Color("39")  // Cyan for user messages
	colorAssistant = lipgloss.Color("212") // Pink for assistant messages
	colorSystem    = lipgloss.Color("214") // Orange for system messages

	// Background colors
	colorBgSubtle = lipgloss.Color("235") // Subtle dark background
	colorBgPanel  = lipgloss.Color("236") // Panel background
)

// Styles contains all the lipgloss styles used throughout the TUI.
type Styles struct {
	// App-level styles
	App        lipgloss.Style
	Header     lipgloss.Style
	Title      lipgloss.Style
	StatusBar  lipgloss.Style
	StatusText lipgloss.Style

	// Message styles
	UserMessage      lipgloss.Style
	UserLabel        lipgloss.Style
	AssistantMessage lipgloss.Style
	AssistantLabel   lipgloss.Style
	SystemMessage    lipgloss.Style
	SystemLabel      lipgloss.Style

	// Error and notification styles
	Error        lipgloss.Style
	ErrorLabel   lipgloss.Style
	Warning      lipgloss.Style
	WarningLabel lipgloss.Style
	Success      lipgloss.Style
	SuccessLabel lipgloss.Style
	Info         lipgloss.Style
	InfoLabel    lipgloss.Style

	// Topic styles
	TopicCurrent  lipgloss.Style
	TopicActive   lipgloss.Style
	TopicArchived lipgloss.Style
	TopicLabel    lipgloss.Style

	// Panel and border styles
	Panel         lipgloss.Style
	PanelTitle    lipgloss.Style
	BorderActive  lipgloss.Style
	BorderInactive lipgloss.Style

	// Input styles
	Input       lipgloss.Style
	InputPrompt lipgloss.Style
	Placeholder lipgloss.Style

	// List and selection styles
	ListItem         lipgloss.Style
	ListItemSelected lipgloss.Style
	ListItemDim      lipgloss.Style

	// Help and keybinding styles
	Help       lipgloss.Style
	HelpKey    lipgloss.Style
	HelpDesc   lipgloss.Style
	HelpSep    lipgloss.Style

	// Debug view styles
	DebugLabel lipgloss.Style
	DebugValue lipgloss.Style
	DebugBar   lipgloss.Style

	// View tab styles
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
}

// DefaultStyles returns the default style configuration.
func DefaultStyles() Styles {
	return Styles{
		// App-level styles
		App: lipgloss.NewStyle().
			Padding(0, 1),

		Header: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(colorMuted).
			Padding(0, 1).
			MarginBottom(1),

		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(colorBgSubtle).
			Padding(0, 1),

		StatusText: lipgloss.NewStyle().
			Foreground(colorMuted),

		// Message styles
		UserMessage: lipgloss.NewStyle().
			Foreground(colorUser).
			PaddingLeft(2),

		UserLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorUser),

		AssistantMessage: lipgloss.NewStyle().
			Foreground(colorAssistant).
			PaddingLeft(2),

		AssistantLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAssistant),

		SystemMessage: lipgloss.NewStyle().
			Foreground(colorSystem).
			Italic(true).
			PaddingLeft(2),

		SystemLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSystem),

		// Error and notification styles
		Error: lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true),

		ErrorLabel: lipgloss.NewStyle().
			Background(colorError).
			Foreground(lipgloss.Color("255")).
			Bold(true).
			Padding(0, 1),

		Warning: lipgloss.NewStyle().
			Foreground(colorWarning),

		WarningLabel: lipgloss.NewStyle().
			Background(colorWarning).
			Foreground(lipgloss.Color("232")).
			Bold(true).
			Padding(0, 1),

		Success: lipgloss.NewStyle().
			Foreground(colorSuccess),

		SuccessLabel: lipgloss.NewStyle().
			Background(colorSuccess).
			Foreground(lipgloss.Color("232")).
			Bold(true).
			Padding(0, 1),

		Info: lipgloss.NewStyle().
			Foreground(colorSecondary),

		InfoLabel: lipgloss.NewStyle().
			Background(colorSecondary).
			Foreground(lipgloss.Color("232")).
			Bold(true).
			Padding(0, 1),

		// Topic styles
		TopicCurrent: lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true),

		TopicActive: lipgloss.NewStyle().
			Foreground(colorSecondary),

		TopicArchived: lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true),

		TopicLabel: lipgloss.NewStyle().
			Background(colorPrimary).
			Foreground(lipgloss.Color("255")).
			Padding(0, 1),

		// Panel and border styles
		Panel: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(1, 2),

		PanelTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Padding(0, 1),

		BorderActive: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary),

		BorderInactive: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted),

		// Input styles
		Input: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(0, 1),

		InputPrompt: lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true),

		Placeholder: lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true),

		// List and selection styles
		ListItem: lipgloss.NewStyle().
			PaddingLeft(2),

		ListItemSelected: lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true).
			PaddingLeft(2),

		ListItemDim: lipgloss.NewStyle().
			Foreground(colorMuted).
			PaddingLeft(2),

		// Help and keybinding styles
		Help: lipgloss.NewStyle().
			Foreground(colorMuted),

		HelpKey: lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true),

		HelpDesc: lipgloss.NewStyle().
			Foreground(colorMuted),

		HelpSep: lipgloss.NewStyle().
			Foreground(colorMuted),

		// Debug view styles
		DebugLabel: lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true).
			Width(20),

		DebugValue: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),

		DebugBar: lipgloss.NewStyle().
			Foreground(colorPrimary),

		// View tab styles
		TabActive: lipgloss.NewStyle().
			Background(colorPrimary).
			Foreground(lipgloss.Color("255")).
			Bold(true).
			Padding(0, 2),

		TabInactive: lipgloss.NewStyle().
			Background(colorBgSubtle).
			Foreground(colorMuted).
			Padding(0, 2),
	}
}

// WithWidth returns a copy of the style with the specified width.
func (s Styles) WithWidth(style lipgloss.Style, width int) lipgloss.Style {
	return style.Width(width)
}

// RenderProgressBar renders a simple progress bar.
func (s Styles) RenderProgressBar(percent float64, width int) string {
	filled := int(float64(width) * percent)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	bar := ""
	for i := range width {
		if i < filled {
			bar += s.DebugBar.Render("█")
		} else {
			bar += s.Help.Render("░")
		}
	}

	return bar
}

// RenderKeyBinding renders a single key binding in the help format.
func (s Styles) RenderKeyBinding(key, desc string) string {
	return s.HelpKey.Render(key) + s.HelpSep.Render(" ") + s.HelpDesc.Render(desc)
}
