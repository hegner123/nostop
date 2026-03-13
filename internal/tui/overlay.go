package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ModalOverlay is the interface satisfied by all dialog types.
// When non-nil on App.activeOverlay, it captures all key input
// and renders on top of the base chat UI.
type ModalOverlay interface {
	OverlayUpdate(msg tea.KeyMsg) (ModalOverlay, tea.Cmd)
	OverlayView(width, height int) string
}

// overlayStyle returns the standard style for overlay dialogs.
func overlayStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 2)
}

// layerOverlay is a positioned content rectangle to render on top of a base layer.
type layerOverlay struct {
	Content string
	X       int
	Y       int
	Width   int
	Height  int
}

// RenderOverlay positions an overlay centered over base content.
func RenderOverlay(base string, overlay ModalOverlay, width, height int) string {
	content := overlay.OverlayView(width, height)
	w := lipgloss.Width(content)
	h := lipgloss.Height(content)
	x := (width - w) / 2
	y := (height - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return compositeLayer(base, layerOverlay{
		Content: content,
		X:       x,
		Y:       y,
		Width:   w,
		Height:  h,
	})
}

// compositeLayer renders the overlay on top of the base string buffer.
// Rows outside the overlay's Y range pass through unchanged.
// Rows inside the overlay's Y range have columns [X, X+Width) replaced
// with the corresponding overlay line.
func compositeLayer(base string, overlay layerOverlay) string {
	baseLines := strings.Split(base, "\n")
	topLines := strings.Split(overlay.Content, "\n")

	// Extend base if overlay extends beyond it
	needed := overlay.Y + overlay.Height
	for len(baseLines) < needed {
		baseLines = append(baseLines, "")
	}

	result := make([]string, len(baseLines))
	for y, baseLine := range baseLines {
		if y >= overlay.Y && y < overlay.Y+overlay.Height {
			topIdx := y - overlay.Y
			if topIdx < len(topLines) {
				result[y] = spliceLine(baseLine, topLines[topIdx], overlay.X, overlay.Width)
			} else {
				result[y] = baseLine
			}
		} else {
			result[y] = baseLine
		}
	}
	return strings.Join(result, "\n")
}

// spliceLine replaces columns [x, x+width) of base with top content.
func spliceLine(base, top string, x, width int) string {
	// Left: base content up to column x
	left := ansi.Truncate(base, x, "")
	leftW := ansi.StringWidth(left)
	if leftW < x {
		left += strings.Repeat(" ", x-leftW)
	}

	// Middle: top content, padded or truncated to exactly `width` columns
	middle := ansi.Truncate(top, width, "")
	middleW := ansi.StringWidth(middle)
	if middleW < width {
		middle += strings.Repeat(" ", width-middleW)
	}

	// Right: base content after column x+width
	right := ansi.TruncateLeft(base, x+width, "")

	return left + middle + right
}
