package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"manygit/internal/git"
)

// titledPanel wraps content in a rounded-border panel whose TOP border embeds a
// lazygit-style "[N] Title" (accent-colored + bold when focused), e.g.
// ╭─[1] Repos────────╮. Box-drawing chars are width-1 in all terminals, so this
// stays aligned. The title lives in the border, not in the content.
func titledPanel(n int, title string, innerW, innerH int, focused bool, content string) string {
	box := panelStyle(innerW, innerH, focused).Render(content)
	lines := strings.Split(box, "\n")
	if len(lines) == 0 || innerW < 6 {
		return box
	}
	bc := lipgloss.Color("240")
	if focused {
		bc = borderAccent
	}
	label := fmt.Sprintf("[%d] %s", n, title) // ASCII, so byte length == display width
	if maxLabel := innerW - 3; len(label) > maxLabel && maxLabel > 0 {
		label = label[:maxLabel]
	}
	fill := innerW - 1 - len(label) // one leading dash + label + fill == innerW between corners
	if fill < 0 {
		fill = 0
	}
	border := lipgloss.NewStyle().Foreground(bc)
	titleStyle := lipgloss.NewStyle().Foreground(bc).Bold(true)
	lines[0] = border.Render("╭─") + titleStyle.Render(label) + border.Render(strings.Repeat("─", fill)+"╮")
	return strings.Join(lines, "\n")
}

// syncGlyph is the concise status token for a repo row. Ahead/behind use ↑/↓
// when unicode=true (nicer, but East-Asian-ambiguous width — may render two
// cells wide and drift columns in some terminals) or alignment-safe ASCII +/-
// when unicode=false. Every other token stays ASCII (always one cell):
//
//	ok in sync · *N dirty (dirtyBadge) · ~ fetching · . loading · ! no upstream/err
func syncGlyph(r *repoVM, unicode bool) string {
	if !r.loaded {
		return styleDim.Render(".")
	}
	if r.fetching {
		return styleDim.Render("~")
	}
	up, down := "+", "-"
	if unicode {
		up, down = "↑", "↓"
	}
	st := r.status
	switch {
	case st.Err != nil, !st.HasUpstream:
		return styleRed.Render("!")
	case st.Ahead > 0 && st.Behind > 0:
		return styleMagenta.Render(fmt.Sprintf("%s%d %s%d", up, st.Ahead, down, st.Behind))
	case st.Ahead > 0:
		return styleYellow.Render(fmt.Sprintf("%s%d", up, st.Ahead))
	case st.Behind > 0:
		return styleCyan.Render(fmt.Sprintf("%s%d", down, st.Behind))
	default:
		return styleGreen.Render("ok")
	}
}

func dirtyBadge(st git.RepoStatus) string {
	if st.DirtyCount > 0 {
		return styleOrange.Render(fmt.Sprintf("*%d", st.DirtyCount))
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

// visibleRepos returns repos matching the current filter (all if empty).
func (m Model) visibleRepos() []*repoVM {
	if m.filter == "" {
		return m.repos
	}
	needle := strings.ToLower(m.filter)
	var out []*repoVM
	for _, r := range m.repos {
		if strings.Contains(strings.ToLower(r.repo.Name), needle) {
			out = append(out, r)
		}
	}
	return out
}

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
	if idx == m.cursor {
		// Always mark the cursor repo so you can tell which repo the Branches/Log
		// panels belong to; highlight it only when the Repos panel is focused.
		if m.focus == panelRepos {
			cursor = styleCursor.Render("> ")
			nameStyle = nameStyle.Foreground(borderAccent).Bold(true)
		} else {
			cursor = styleDim.Render("> ")
		}
	}
	nameCell := nameStyle.Render(truncate(r.repo.Name, nameW))
	dirtyCell := lipgloss.NewStyle().Width(wDirty).Render(dirtyBadge(r.status))
	statusCell := lipgloss.NewStyle().Width(wStatus).Render(syncGlyph(r, m.cfg.UnicodeGlyphs()))
	// Two spaces after the cursor keep the column budget (computeDims) unchanged
	// now that the selection marker is gone.
	return cursor + "  " + nameCell + " " + dirtyCell + " " + statusCell
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

func (m Model) renderBranches(contentW int) string {
	var b strings.Builder
	for i, br := range m.branches {
		cursor := "  "
		if m.focus == panelBranches && i == m.branchCursor {
			cursor = styleCursor.Render("> ")
		}
		name := br.Name
		if br.IsRemote {
			name = styleDim.Render(name)
		}
		if br.IsCurrent {
			name += styleGreen.Render(" (current)")
		}
		b.WriteString(cursor + name + "\n")
	}
	// Truncate each line to the panel content width so long branch names don't
	// wrap and misalign the panel (MaxWidth is ANSI-aware).
	return lipgloss.NewStyle().MaxWidth(contentW).Render(b.String())
}

func (m Model) renderLog(contentW int) string {
	var b strings.Builder
	for _, line := range m.log {
		b.WriteString(line + "\n")
	}
	// Truncate long graph-log lines to the panel content width (no wrap).
	return lipgloss.NewStyle().MaxWidth(contentW).Render(b.String())
}

func (m Model) footer() string {
	return styleDim.Render(
		"space branches | s sync | p push | b checkout | o open | r refetch | ? help | q quit")
}

func (m Model) statusOrFilterLine() string {
	if m.filtering {
		return styleYellow.Render("/" + m.filter + "_")
	}
	if m.statusLine != "" {
		return m.statusLine
	}
	return m.footer()
}

// helpView renders a full-screen help overlay: keybindings and the status
// legend (what ^N / vN / *N / ok mean).
func (m Model) helpView() string {
	row := func(left, desc string) string {
		return "  " + lipgloss.NewStyle().Width(14).Render(left) + styleDim.Render(desc)
	}
	up, down := "+", "-"
	if m.cfg.UnicodeGlyphs() {
		up, down = "↑", "↓"
	}
	lines := []string{
		styleTitle.Render("manygit — help"),
		"",
		styleGroup.Render("Panels & navigation"),
		row("1 / 2 / 3", "focus the Repos / Branches / Log panel"),
		row("tab", "cycle panels"),
		row("j / k", "move within the FOCUSED panel"),
		row("space", "jump to the current repo's branches (space again = back)"),
		row("/", "filter repos by name (esc clears)"),
		"",
		styleGroup.Render("Actions") + styleDim.Render("  — apply to the highlighted (>) repo"),
		row("s", "sync: fetch + pull --ff-only   (dirty repos skipped)"),
		row("p", "push"),
		row("f / r", "fetch current repo / refetch all"),
		row("b / enter", "checkout the selected branch (in the Branches panel)"),
		row("o", "open the current repo in your editor"),
		"",
		styleGroup.Render("Status column"),
		row(styleGreen.Render("ok"), "up to date with its upstream"),
		row(styleYellow.Render(up+"N"), "ahead N — you have commits to PUSH"),
		row(styleCyan.Render(down+"N"), "behind N — commits available to PULL"),
		row(styleMagenta.Render(up+"N "+down+"M"), "diverged (N ahead, M behind)"),
		row(styleOrange.Render("*N"), "N files changed (dirty working tree)"),
		row(styleDim.Render("~ / ."), "fetching / loading"),
		row(styleRed.Render("!"), "no upstream, or error"),
		"",
		styleDim.Render("  ? or esc  close help        q  quit"),
	}
	tw, th := m.width, m.height
	if tw <= 0 {
		tw = minTermW
	}
	if th <= 0 {
		th = minTermH
	}
	box := panelStyle(tw-2, th-2, true).Render(clampLines(strings.Join(lines, "\n"), th-2))
	return lipgloss.NewStyle().MaxWidth(tw).Render(box)
}

func (m Model) View() string {
	if m.showHelp {
		return m.helpView()
	}
	d := computeDims(m.width, m.height)
	title := styleTitle.Render("manygit") + "  " +
		styleDim.Render(fmt.Sprintf("%d repos", len(m.repos)))

	left := titledPanel(1, "Repos", d.leftW, d.bodyH, m.focus == panelRepos,
		lipgloss.NewStyle().MaxWidth(d.leftW-2).Render(clampLines(m.renderRepoBody(d), d.bodyH)))

	// right column: two stacked panels sharing the left panel's total height.
	topInner := max((d.bodyH-2)*40/100, 3)
	botInner := max((d.bodyH-2)-topInner, 3)
	branches := titledPanel(2, "Branches", d.rightW, topInner, m.focus == panelBranches,
		clampLines(m.renderBranches(d.rightW-2), topInner))
	logp := titledPanel(3, "Log", d.rightW, botInner, m.focus == panelLog,
		clampLines(m.renderLog(d.rightW-2), botInner))
	right := lipgloss.JoinVertical(lipgloss.Left, branches, logp)

	cols := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gutter), right)
	view := lipgloss.JoinVertical(lipgloss.Left, title, "", cols, m.statusOrFilterLine())

	tw := m.width
	if tw <= 0 {
		tw = minTermW
	}
	return lipgloss.NewStyle().MaxWidth(tw).Render(view)
}
