package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"manygit/internal/git"
)

// titledPanel wraps content in a rounded-border panel whose TOP border embeds a
// lazygit-style "[N] Title", e.g. ╭─[1] Repos────────╮.
func titledPanel(n int, title string, innerW, innerH int, focused bool, content string) string {
	return titledBox(fmt.Sprintf("[%d] %s", n, title), innerW, innerH, focused, content)
}

// titledBox is titledPanel with a raw label (ASCII) spliced into the top border.
// Box-drawing chars are width-1 in all terminals, so this stays aligned.
func titledBox(label string, innerW, innerH int, focused bool, content string) string {
	box := panelStyle(innerW, innerH, focused).Render(content)
	lines := strings.Split(box, "\n")
	if len(lines) == 0 || innerW < 6 {
		return box
	}
	bc := lipgloss.Color("240")
	if focused {
		bc = borderAccent
	}
	// Measure by display width, not bytes — the label may include a repo name
	// with non-ASCII characters. Truncate rune-safely so the border stays aligned.
	if maxLabel := innerW - 3; lipgloss.Width(label) > maxLabel && maxLabel > 0 {
		if rl := []rune(label); len(rl) > maxLabel {
			label = string(rl[:maxLabel])
		}
	}
	fill := innerW - 1 - lipgloss.Width(label) // leading dash + label + fill == innerW between corners
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

// visibleRepos returns repos matching the active filters: the name filter (`/`)
// and/or the "needs attention" filter (`F`). Both compose (AND).
func (m Model) visibleRepos() []*repoVM {
	if m.filter == "" && !m.filterAttention {
		return m.repos
	}
	needle := strings.ToLower(m.filter)
	var out []*repoVM
	for _, r := range m.repos {
		if needle != "" && !strings.Contains(strings.ToLower(r.repo.Name), needle) {
			continue
		}
		if m.filterAttention && !needsAttention(r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// needsAttention reports whether a repo has uncommitted changes or is out of
// sync with its upstream (ahead/behind) — something to commit, pull, or push.
func needsAttention(r *repoVM) bool {
	st := r.status
	return st.DirtyCount > 0 || st.Ahead > 0 || st.Behind > 0
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
		// Cap the display name — many branches are long Jira-generated names.
		name := br.Name
		if r := []rune(name); len(r) > branchNameMax {
			name = string(r[:branchNameMax-2]) + ".."
		}
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

// renderBottom renders the active view of the multi-view bottom slot.
func (m Model) renderBottom(contentW, innerH int) string {
	switch m.bottomView {
	case bvChanges:
		return m.renderChangesView(contentW, innerH)
	case bvOutput:
		return m.renderOutputView(contentW, innerH)
	default:
		return m.renderGraphView(contentW, innerH)
	}
}

// window returns lines[start:end] so that keepVisible sits inside a height-h
// window, and the start index.
func window(n, keepVisible, h int) (start, end int) {
	if keepVisible >= h {
		start = keepVisible - h + 1
	}
	end = start + h
	if end > n {
		end = n
	}
	if start > n {
		start = n
	}
	return start, end
}

// renderGraphView shows the colored graph with a synthetic WIP entry on top and
// a selection cursor that snaps to commits; the selected entry stays visible.
func (m Model) renderGraphView(contentW, innerH int) string {
	focused := m.focus == panelBottom
	texts := []string{styleYellow.Render("WIP (uncommitted changes)")}
	texts = append(texts, m.graphLines...)

	selIdx := 0 // render index of the selected entry (0 = WIP)
	if m.graphSel >= 1 && m.graphSel-1 < len(m.graphCommits) {
		selIdx = m.graphCommits[m.graphSel-1].Line + 1 // +1 for the WIP row
	}
	start, end := window(len(texts), selIdx, innerH)

	var b strings.Builder
	for i := start; i < end; i++ {
		cur := "  "
		if focused && i == selIdx {
			cur = styleCursor.Render("> ")
		}
		b.WriteString(cur + texts[i] + "\n")
	}
	return lipgloss.NewStyle().MaxWidth(contentW).Render(b.String())
}

// colorStatus colors a git status letter (A/D/M/R/??).
func colorStatus(s string) string {
	switch {
	case s == "A" || s == "??":
		return styleGreen.Render(s)
	case s == "D":
		return styleRed.Render(s)
	case strings.HasPrefix(s, "R"):
		return styleCyan.Render(s)
	default:
		return styleYellow.Render(s)
	}
}

// renderChangesView shows the changed files of the selected graph entry, or the
// selected file's in-place diff.
func (m Model) renderChangesView(contentW, innerH int) string {
	if m.changeShowDiff {
		start, end := window(len(m.changeDiff), m.changeDiffOff, innerH)
		return lipgloss.NewStyle().MaxWidth(contentW).Render(strings.Join(m.changeDiff[start:end], "\n"))
	}
	if len(m.changeFiles) == 0 {
		what := "working tree"
		if ref := m.selectedRef(); ref != "" {
			what = "commit " + ref[:min(7, len(ref))]
		}
		return styleDim.Render("(no changes in " + what + ")")
	}
	focused := m.focus == panelBottom
	start, end := window(len(m.changeFiles), m.changeCursor, innerH)
	var b strings.Builder
	for i := start; i < end; i++ {
		f := m.changeFiles[i]
		cur := "  "
		if focused && i == m.changeCursor {
			cur = styleCursor.Render("> ")
		}
		sc := f.Status // already short, but cap so the Width(3) cell can't wrap
		if len(sc) > 2 {
			sc = sc[:2]
		}
		st := lipgloss.NewStyle().Width(3).Render(colorStatus(sc))
		b.WriteString(cur + st + f.Path + "\n")
	}
	return lipgloss.NewStyle().MaxWidth(contentW).Render(b.String())
}

// renderOutputView shows captured script output (Phase 2).
func (m Model) renderOutputView(contentW, innerH int) string {
	if len(m.outputLines) == 0 {
		return styleDim.Render("(run a script from [2] Scripts to see its output here)")
	}
	start, end := window(len(m.outputLines), m.outputOffset, innerH)
	return lipgloss.NewStyle().MaxWidth(contentW).Render(strings.Join(m.outputLines[start:end], "\n"))
}

func (m Model) renderScripts(contentW int) string {
	if len(m.scripts) == 0 {
		return styleDim.Render("(no .sh scripts found)")
	}
	var b strings.Builder
	for i, s := range m.scripts {
		cursor := "  "
		if m.focus == panelScripts && i == m.scriptCursor {
			cursor = styleCursor.Render("> ")
		}
		b.WriteString(cursor + s.Name + "\n")
	}
	return lipgloss.NewStyle().MaxWidth(contentW).Render(b.String())
}

func (m Model) footer() string {
	space := "space branches"
	if m.focus == panelScripts {
		space = "space run"
	}
	return styleDim.Render(
		space + " | g graph | F changed | s sync | p push | o open | r refetch | ? help | q quit")
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
		row("1 / 2 / 3", "focus Repos / Scripts / Branches"),
		row("4 / 5 / 6", "bottom slot: Graph / Changes / Output"),
		row("tab", "cycle panels"),
		row("j / k", "move within the FOCUSED panel"),
		row("space", "Repos → branches · Scripts → run it · else back to Repos"),
		row("g", "full-screen colored commit graph (j/k scroll · esc closes)"),
		row("F", "toggle: show only changed / out-of-sync repos"),
		row("/", "filter repos by name (esc clears)"),
		"",
		styleGroup.Render("Graph (4) & Changes (5)"),
		row("4  j/k", "select a commit — or WIP (uncommitted) at the top"),
		row("5", "show the selected entry's changed files"),
		row("5  enter", "open the highlighted file's diff in-place (esc = back)"),
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

// graphView renders a full-screen colored commit graph with j/k scrolling.
func (m Model) graphView() string {
	tw, th := m.width, m.height
	if tw <= 0 {
		tw = minTermW
	}
	if th <= 0 {
		th = minTermH
	}
	name := "(no repo)"
	if r := m.currentVisible(m.visibleRepos()); r != nil {
		name = r.repo.Name
	}
	innerH := th - 2 // border rows
	if innerH < 3 {
		innerH = 3
	}
	content := styleDim.Render("(loading graph…)")
	if len(m.graphLines) > 0 {
		start := m.graphOffset
		if start > len(m.graphLines)-1 {
			start = len(m.graphLines) - 1
		}
		if start < 0 {
			start = 0
		}
		end := start + innerH
		if end > len(m.graphLines) {
			end = len(m.graphLines)
		}
		content = lipgloss.NewStyle().MaxWidth(tw - 4).Render(strings.Join(m.graphLines[start:end], "\n"))
	}
	box := titledBox("Graph: "+name+"  (j/k scroll, esc close)", tw-2, innerH, true, content)
	return lipgloss.NewStyle().MaxWidth(tw).Render(box)
}

func (m Model) View() string {
	if m.showGraph {
		return m.graphView()
	}
	if m.showHelp {
		return m.helpView()
	}
	d := computeDims(m.width, m.height)
	count := fmt.Sprintf("%d repos", len(m.repos))
	if m.filterAttention || m.filter != "" {
		count = fmt.Sprintf("%d of %d repos", len(m.visibleRepos()), len(m.repos))
	}
	title := styleTitle.Render("manygit") + "  " + styleDim.Render(count)
	if m.filterAttention {
		title += "  " + styleYellow.Render("[changed / unsynced]")
	}

	// left column: Repos (large) over a small Scripts panel; the two share the
	// column's total height, matching the right column.
	scriptsInner := len(m.scripts)
	if scriptsInner < 3 {
		scriptsInner = 3
	}
	if maxS := (d.bodyH - 2) / 3; scriptsInner > maxS {
		scriptsInner = maxS
	}
	reposInner := max((d.bodyH-2)-scriptsInner, 3)
	reposPanel := titledPanel(1, "Repos", d.leftW, reposInner, m.focus == panelRepos,
		lipgloss.NewStyle().MaxWidth(d.leftW-2).Render(clampLines(m.renderRepoBody(d), reposInner)))
	scriptsPanel := titledPanel(2, "Scripts", d.leftW, scriptsInner, m.focus == panelScripts,
		clampLines(m.renderScripts(d.leftW-2), scriptsInner))
	left := lipgloss.JoinVertical(lipgloss.Left, reposPanel, scriptsPanel)

	// right column: two stacked panels sharing the left panel's total height.
	topInner := max((d.bodyH-2)*40/100, 3)
	botInner := max((d.bodyH-2)-topInner, 3)
	branches := titledPanel(3, "Branches", d.rightW, topInner, m.focus == panelBranches,
		clampLines(m.renderBranches(d.rightW-2), topInner))
	// bottom multi-view slot: 4 Graph / 5 Changes / 6 Output
	bnum, btitle := 4, "Graph"
	switch m.bottomView {
	case bvChanges:
		bnum, btitle = 5, "Changes"
	case bvOutput:
		bnum, btitle = 6, "Output"
	}
	bottom := titledPanel(bnum, btitle, d.rightW, botInner, m.focus == panelBottom,
		clampLines(m.renderBottom(d.rightW-2, botInner), botInner))
	right := lipgloss.JoinVertical(lipgloss.Left, branches, bottom)

	cols := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gutter), right)
	view := lipgloss.JoinVertical(lipgloss.Left, title, "", cols, m.statusOrFilterLine())

	tw := m.width
	if tw <= 0 {
		tw = minTermW
	}
	return lipgloss.NewStyle().MaxWidth(tw).Render(view)
}
