package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"manygit/internal/git"
)

// syncGlyph is the concise status glyph for a repo row.
func syncGlyph(r *repoVM) string {
	if !r.loaded {
		return styleDim.Render("…")
	}
	if r.fetching {
		return styleDim.Render("⟳")
	}
	st := r.status
	switch {
	case st.Err != nil, !st.HasUpstream:
		return styleRed.Render("⚠")
	case st.Ahead > 0 && st.Behind > 0:
		return styleMagenta.Render(fmt.Sprintf("⇕↑%d↓%d", st.Ahead, st.Behind))
	case st.Ahead > 0:
		return styleYellow.Render(fmt.Sprintf("↑%d", st.Ahead))
	case st.Behind > 0:
		return styleCyan.Render(fmt.Sprintf("↓%d", st.Behind))
	default:
		return styleGreen.Render("✓")
	}
}

func dirtyBadge(st git.RepoStatus) string {
	if st.DirtyCount > 0 {
		return styleOrange.Render(fmt.Sprintf("●%d", st.DirtyCount))
	}
	return ""
}

// truncate shortens s to at most w display cells (repo names are plain ASCII).
func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

// clampLines caps s to maxLines so content never overflows a panel's height.
func clampLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n")
}

// visibleRepos is the display list (all repos in Task 7; filtered in Task 8).
func (m Model) visibleRepos() []*repoVM { return m.repos }

// currentVisible is the highlighted repo within the visible slice.
func (m Model) currentVisible(vis []*repoVM) *repoVM {
	if m.cursor < 0 || m.cursor >= len(vis) {
		return nil
	}
	return vis[m.cursor]
}

// renderRow composes one repo row from fixed-width, ANSI-aware cells so wide
// glyphs never break alignment.
func (m Model) renderRow(idx int, r *repoVM, nameW int) string {
	cursor := "  "
	nameStyle := lipgloss.NewStyle().Width(nameW)
	if m.focus == panelRepos && idx == m.cursor {
		cursor = styleCursor.Render("▸ ")
		nameStyle = nameStyle.Foreground(borderAccent).Bold(true)
	}
	mark := " "
	if m.selected[r.repo.Path] {
		mark = styleGreen.Render("✔")
	}
	nameCell := nameStyle.Render(truncate(r.repo.Name, nameW))
	dirtyCell := lipgloss.NewStyle().Width(wDirty).Render(dirtyBadge(r.status))
	statusCell := lipgloss.NewStyle().Width(wStatus).Render(syncGlyph(r))
	return cursor + mark + " " + nameCell + " " + dirtyCell + " " + statusCell
}

func (m Model) renderRepoBody(d dims) string {
	var b strings.Builder
	lastGroup := ""
	for i, r := range m.visibleRepos() {
		if r.repo.Group != lastGroup {
			b.WriteString(styleGroup.Render(r.repo.Group) + "\n")
			lastGroup = r.repo.Group
		}
		b.WriteString(m.renderRow(i, r, d.nameW) + "\n")
	}
	return b.String()
}

func (m Model) footer() string {
	return styleDim.Render(
		"space select · s sync · p push · b checkout · o open · r refetch · ? help · q quit")
}

func (m Model) View() string {
	d := computeDims(m.width, m.height)
	title := styleTitle.Render("manygit") + "  " +
		styleDim.Render(fmt.Sprintf("%d repos", len(m.repos)))
	left := panelStyle(d.leftW, d.bodyH, m.focus == panelRepos).
		Render(clampLines(m.renderRepoBody(d), d.bodyH))
	return lipgloss.JoinVertical(lipgloss.Left, title, "", left, m.footer())
}
