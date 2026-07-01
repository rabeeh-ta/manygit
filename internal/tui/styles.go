package tui

import "github.com/charmbracelet/lipgloss"

// borderAccent highlights the focused panel border and the cursor.
var borderAccent = lipgloss.Color("39")

var (
	styleGreen   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleYellow  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleCyan    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleMagenta = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleOrange  = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	styleRed     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleGroup   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	styleCursor  = lipgloss.NewStyle().Bold(true).Foreground(borderAccent)
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(borderAccent)
)

// Fixed column widths (display cells) reserve room for the widest glyph strings
// so columns never shift when a glyph renders two cells wide.
const (
	wDirty        = 4  // "●99"
	wStatus       = 10 // "⇕↑9↓9"
	branchNameMax = 15 // truncate long (Jira-generated) branch names in the panel
)

// panelStyle is a bordered, padded container. Width/Height set the INNER size
// (content + padding); the rounded border adds one cell on each side, so the
// total rendered size is Width+2 by Height+2 — callers budget that.
func panelStyle(innerW, innerH int, focused bool) lipgloss.Style {
	bc := lipgloss.Color("240")
	if focused {
		bc = borderAccent
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(bc).
		Padding(0, 1).
		Width(innerW).
		Height(innerH)
}
