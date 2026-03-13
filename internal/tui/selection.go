package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

const (
	doubleClickThreshold = 400 * time.Millisecond
	clickTolerance       = 2
	scrollDelta          = 3
)

// copyDoneMsg is sent after clipboard copy completes.
type copyDoneMsg struct{}

// TextSelection manages mouse-driven text selection state for the chat viewport.
type TextSelection struct {
	active    bool
	mouseDown bool

	// Selection endpoints in viewport-visible coordinates (row, col).
	// These are relative to the top of the viewport's visible area.
	startLine, startCol int
	endLine, endCol     int

	// Multi-click detection
	clickCount    int
	lastClickTime time.Time
	lastClickX    int
	lastClickY    int
}

// NewTextSelection creates a new TextSelection.
func NewTextSelection() *TextSelection {
	return &TextSelection{}
}

// HasSelection returns whether there is a non-empty selection.
func (s *TextSelection) HasSelection() bool {
	if !s.active {
		return false
	}
	return s.startLine != s.endLine || s.startCol != s.endCol
}

// Clear resets all selection state.
func (s *TextSelection) Clear() {
	s.active = false
	s.mouseDown = false
	s.startLine = 0
	s.startCol = 0
	s.endLine = 0
	s.endCol = 0
}

// HandleMouseDown processes a left-click at viewport-relative (col, row).
// Returns the click count (1=single, 2=double, 3=triple) for the caller
// to trigger word or line selection.
func (s *TextSelection) HandleMouseDown(col, row int) int {
	now := time.Now()
	if now.Sub(s.lastClickTime) <= doubleClickThreshold &&
		intAbs(col-s.lastClickX) <= clickTolerance &&
		intAbs(row-s.lastClickY) <= clickTolerance {
		s.clickCount++
	} else {
		s.clickCount = 1
	}
	s.lastClickTime = now
	s.lastClickX = col
	s.lastClickY = row

	s.mouseDown = true
	s.active = true
	s.startLine = row
	s.startCol = col
	s.endLine = row
	s.endCol = col

	return s.clickCount
}

// HandleMouseDrag updates the selection endpoint during a drag.
func (s *TextSelection) HandleMouseDrag(col, row int) {
	if !s.mouseDown {
		return
	}
	s.endLine = row
	s.endCol = col
}

// HandleMouseUp finalises the selection. Returns true if a non-empty
// selection exists and should be copied.
func (s *TextSelection) HandleMouseUp() bool {
	wasDown := s.mouseDown
	s.mouseDown = false
	return wasDown && s.HasSelection()
}

// NormalizedRange returns the selection with start <= end.
func (s *TextSelection) NormalizedRange() (startLine, startCol, endLine, endCol int) {
	if s.startLine < s.endLine ||
		(s.startLine == s.endLine && s.startCol <= s.endCol) {
		return s.startLine, s.startCol, s.endLine, s.endCol
	}
	return s.endLine, s.endCol, s.startLine, s.startCol
}

// SelectWord sets the selection to word boundaries around (col) on the
// given line content at viewport row lineY.
func (s *TextSelection) SelectWord(line string, lineY, col int) {
	stripped := ansi.Strip(line)
	start, end := findWordBounds(stripped, col)
	if start == end {
		return // clicked on whitespace
	}
	s.startLine = lineY
	s.startCol = start
	s.endLine = lineY
	s.endCol = end
	s.active = true
	s.mouseDown = true // release will trigger copy
}

// SelectLine selects the entire visible line at viewport row lineY.
func (s *TextSelection) SelectLine(line string, lineY int) {
	s.startLine = lineY
	s.startCol = 0
	s.endLine = lineY
	s.endCol = ansi.StringWidth(line)
	s.active = true
	s.mouseDown = true
}

// ExtractText returns the plain text within the selection.
// contentLines are all content lines (not just visible); yOffset is the
// viewport scroll offset used to map viewport rows to content rows.
func (s *TextSelection) ExtractText(contentLines []string, yOffset int) string {
	if !s.HasSelection() {
		return ""
	}

	sLine, sCol, eLine, eCol := s.NormalizedRange()
	// Convert viewport-relative rows to content rows.
	sLine += yOffset
	eLine += yOffset

	var sb strings.Builder
	for i := sLine; i <= eLine && i < len(contentLines); i++ {
		stripped := ansi.Strip(contentLines[i])
		runes := []rune(stripped)

		colStart := 0
		if i == sLine {
			colStart = sCol
		}
		colEnd := len(runes)
		if i == eLine {
			colEnd = min(eCol, len(runes))
		}
		if colStart > len(runes) {
			colStart = len(runes)
		}

		if colStart < colEnd {
			sb.WriteString(string(runes[colStart:colEnd]))
		}
		if i < eLine {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// ApplyHighlight post-processes the viewport's rendered output, applying
// ANSI reverse video to the selected region.
func (s *TextSelection) ApplyHighlight(viewportOutput string) string {
	if !s.HasSelection() {
		return viewportOutput
	}

	sLine, sCol, eLine, eCol := s.NormalizedRange()
	lines := strings.Split(viewportOutput, "\n")

	// Clamp to valid line range — mouse drag can produce negative rows
	// when the cursor moves above the viewport.
	if sLine < 0 {
		sLine = 0
		sCol = 0
	}
	if eLine < 0 {
		return viewportOutput
	}

	for i := sLine; i <= eLine && i < len(lines); i++ {
		colStart := 0
		colEnd := ansi.StringWidth(lines[i])

		if i == sLine {
			colStart = sCol
		}
		if i == eLine {
			colEnd = eCol
		}

		if colStart < colEnd && colStart < ansi.StringWidth(lines[i]) {
			lines[i] = highlightLineRange(lines[i], colStart, colEnd)
		}
	}

	return strings.Join(lines, "\n")
}

// highlightLineRange applies ANSI reverse video to columns [start, end).
func highlightLineRange(line string, start, end int) string {
	lineWidth := ansi.StringWidth(line)
	if start >= lineWidth || start >= end {
		return line
	}
	if end > lineWidth {
		end = lineWidth
	}

	left := ansi.Truncate(line, start, "")
	rightPart := ansi.TruncateLeft(line, start, "")
	middle := ansi.Truncate(rightPart, end-start, "")
	right := ansi.TruncateLeft(line, end, "")

	return left + "\x1b[7m" + middle + "\x1b[27m" + right
}

// CopyToClipboard copies text via OSC 52 (terminal) and native clipboard.
func CopyToClipboard(text string) tea.Cmd {
	return tea.Sequence(
		tea.SetClipboard(text),
		func() tea.Msg {
			_ = clipboard.WriteAll(text)
			return nil
		},
		func() tea.Msg {
			return copyDoneMsg{}
		},
	)
}

// findWordBounds locates the start and end column of the word at col.
// Returns (col, col) if the position is whitespace.
func findWordBounds(line string, col int) (start, end int) {
	runes := []rune(line)
	if col >= len(runes) || col < 0 {
		return col, col
	}

	if isWordBreak(runes[col]) {
		return col, col
	}

	start = col
	for start > 0 && !isWordBreak(runes[start-1]) {
		start--
	}

	end = col
	for end < len(runes) && !isWordBreak(runes[end]) {
		end++
	}

	return start, end
}

func isWordBreak(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '.', ',', ';', ':', '(', ')', '[', ']',
		'{', '}', '"', '\'', '`', '<', '>', '/', '\\', '|', '!', '?':
		return true
	}
	return false
}

func intAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
