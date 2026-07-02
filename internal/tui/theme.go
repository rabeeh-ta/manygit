package tui

import "github.com/charmbracelet/lipgloss"

// Theme is a named palette for manygit's "chrome" colors: the accent (focused
// borders, cursor ">", panel titles), group headers, dim text (dividers,
// inactive tabs, secondary text) and error. The semantic status colors
// (ok/ahead/behind/dirty) are intentionally NOT themed so they stay readable
// across themes. Palettes are adapted from monkeytype's theme set
// (github.com/monkeytypegame/monkeytype, frontend/src/ts/constants/themes.ts):
// Accent<-main, Dim<-sub, Error<-error.
type Theme struct {
	Name   string
	Accent lipgloss.Color
	Group  lipgloss.Color
	Dim    lipgloss.Color
	Error  lipgloss.Color
}

// themeList holds the built-in themes in display order. The first entry is the
// default — manygit's original ANSI palette; changing it would change the
// out-of-the-box look, so keep its values in sync with styles.go's literals.
var themeList = []Theme{
	{Name: "default", Accent: "39", Group: "4", Dim: "240", Error: "1"},
	{Name: "serika_dark", Accent: "#e2b714", Group: "#e2b714", Dim: "#646669", Error: "#ca4754"},
	{Name: "dracula", Accent: "#bd93f9", Group: "#bd93f9", Dim: "#6272a4", Error: "#ff5555"},
	{Name: "nord", Accent: "#88c0d0", Group: "#88c0d0", Dim: "#929aaa", Error: "#bf616a"},
	{Name: "catppuccin", Accent: "#cba6f7", Group: "#cba6f7", Dim: "#7f849c", Error: "#f38ba8"},
	{Name: "8008", Accent: "#f44c7f", Group: "#f44c7f", Dim: "#939eae", Error: "#da3333"},
}

// themeByName returns the named theme, or the default (index 0) if unknown.
func themeByName(name string) Theme {
	for _, t := range themeList {
		if t.Name == name {
			return t
		}
	}
	return themeList[0]
}

// themeIndex returns the index of the named theme in themeList, or 0 if unknown.
func themeIndex(name string) int {
	for i, t := range themeList {
		if t.Name == name {
			return i
		}
	}
	return 0
}

// applyTheme sets the themeable package-level styles from t, rebuilding the
// styles derived from the accent. Safe to call repeatedly (on startup and on a
// live theme change).
func applyTheme(t Theme) {
	borderAccent = t.Accent
	styleCursor = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
	styleGroup = lipgloss.NewStyle().Bold(true).Foreground(t.Group)
	styleDim = lipgloss.NewStyle().Foreground(t.Dim)
	styleRed = lipgloss.NewStyle().Foreground(t.Error)
}
