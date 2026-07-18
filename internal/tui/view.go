package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"manygit/internal/config"
	"manygit/internal/git"
	"manygit/internal/harness"
)

// decoRefRe matches one colored token — <set-color><text><reset> — as emitted by
// `git log --decorate --color=always` (reset is `\x1b[m`, or `\x1b[0m` on some
// gits). graphRefMax caps the text length. This also matches the colored short
// hash, but that's well under the cap (--oneline always abbreviates), so only
// long ref names are ever shortened.
var decoRefRe = regexp.MustCompile(`(\x1b\[[0-9;]*m)([^\x1b]+)(\x1b\[0?m)`)

const graphRefMax = 15

// shortenGraphRefs caps long ref names inside a colored graph line's decorations
// so a long branch name can't push the commit subject off-screen. Short tokens and
// the uncolored commit subject are left untouched.
func shortenGraphRefs(line string) string {
	return decoRefRe.ReplaceAllStringFunc(line, func(tok string) string {
		g := decoRefRe.FindStringSubmatch(tok)
		text := []rune(g[2])
		if len(text) <= graphRefMax {
			return tok
		}
		return g[1] + string(text[:graphRefMax-2]) + ".." + g[3]
	})
}

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

// titledBarBox is titledBox for a label the caller has already styled (e.g. the
// bottom slot's colored tab bar): it does not recolor the label, and truncates
// ANSI-aware so embedded colors survive at narrow widths.
func titledBarBox(label string, innerW, innerH int, focused bool, content string) string {
	box := panelStyle(innerW, innerH, focused).Render(content)
	lines := strings.Split(box, "\n")
	if len(lines) == 0 || innerW < 6 {
		return box
	}
	bc := lipgloss.Color("240")
	if focused {
		bc = borderAccent
	}
	if maxLabel := innerW - 3; maxLabel > 0 && lipgloss.Width(label) > maxLabel {
		label = lipgloss.NewStyle().MaxWidth(maxLabel).Render(label)
	}
	fill := innerW - 1 - lipgloss.Width(label)
	if fill < 0 {
		fill = 0
	}
	border := lipgloss.NewStyle().Foreground(bc)
	lines[0] = border.Render("╭─") + label + border.Render(strings.Repeat("─", fill)+"╮")
	return strings.Join(lines, "\n")
}

// syncGlyph is the concise status token for a repo row. Ahead/behind use ↑/↓
// when unicode=true (nicer, but East-Asian-ambiguous width — may render two
// cells wide and drift columns in some terminals) or alignment-safe ASCII +/-
// when unicode=false. Every other token stays ASCII (always one cell):
//
//	ok in sync · *N dirty (dirtyBadge) · ~ fetching · . loading · no-remote local-only · ! no upstream/err
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
	case st.Err != nil:
		return styleRed.Render("!")
	case !st.HasUpstream:
		// Nothing to compare against. Two very different reasons: the repo has no
		// remote at all (local-only — dim, nothing is wrong with it), or it has one
		// and this branch was simply never pushed (!, actionable).
		if !st.HasRemote {
			return styleDim.Render("no-remote")
		}
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

// truncate shortens s to at most w display cells, appending "…" when it cuts.
// Width-aware: wide (CJK/emoji) runes count as their real cell width, so the
// result never exceeds w cells (git branch names are not guaranteed ASCII).
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	budget, tail := w-1, "…" // reserve one cell for the ellipsis
	if budget <= 0 {
		budget, tail = w, "" // only room for content, no tail
	}
	var out strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > budget {
			break
		}
		out.WriteRune(r)
		used += rw
	}
	return out.String() + tail
}

// clampLines caps s to maxLines so content never overflows a panel's height.
func clampLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n")
}

// repoHaystack is the text `/` matches a repo row against: what the row shows —
// its name and current branch, plus the latest tag while `t` has tags inline.
// It matches the full values rather than the width-truncated ones renderRow
// draws, so results never depend on how wide the terminal happens to be.
//
// The group header and the dirty/sync cells are deliberately left out: `F`
// already filters on attention state, and folding it into `/` would give "ok"
// two meanings.
func (m Model) repoHaystack(r *repoVM) string {
	s := r.repo.Name + " " + currentBranch(r.status)
	if m.showTagsInline {
		s += " " + r.latestTag
	}
	return strings.ToLower(s)
}

// visibleRepos returns repos matching the active filters: the row filter (`/`)
// and/or the "needs attention" filter (`F`). Both compose (AND).
func (m Model) visibleRepos() []*repoVM {
	needle := ""
	if m.filterPanel == panelRepos {
		needle = strings.ToLower(m.filter)
	}
	if needle == "" && !m.filterAttention {
		return m.repos
	}
	var out []*repoVM
	for _, r := range m.repos {
		if needle != "" && !strings.Contains(m.repoHaystack(r), needle) {
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

// currentBranch is the branch label to show for a repo, or "" if unknown.
func currentBranch(st git.RepoStatus) string {
	if st.Detached {
		return "detached" // st.Branch is "(detached)"; avoid doubling the parens
	}
	return st.Branch
}

// fitNameSuffixes fits "name (branch) (tag)" into the name column of width w.
// The name keeps priority; the branch is truncated to fill whatever room is left
// (or dropped when there's no room); the tag is shown only if it fits whole (it
// is never truncated — a partial tag is meaningless), and only when the branch
// didn't have to be truncated. Any argument may be "".
func fitNameSuffixes(name, branch, tag string, w int) (outName, outBranch, outTag string) {
	if lipgloss.Width(name) > w {
		return truncate(name, w), "", ""
	}
	const wrap = 3 // " (" + ")"
	used := lipgloss.Width(name)
	if branch != "" {
		switch {
		case used+wrap+lipgloss.Width(branch) <= w:
			outBranch = branch
			used += wrap + lipgloss.Width(branch)
		case w-used-wrap >= 3:
			// Not enough for the whole branch — truncate it to fill the rest.
			// Nothing is left for the tag afterward.
			return name, truncate(branch, w-used-wrap), ""
		default:
			return name, "", "" // no room for even a short branch
		}
	}
	if tag != "" && used+wrap+lipgloss.Width(tag) <= w {
		outTag = tag
	}
	return name, outBranch, outTag
}

// renderRow composes one repo row from fixed-width, ANSI-aware cells so wide
// glyphs never break alignment.
func (m Model) renderRow(idx int, r *repoVM, nameW int) string {
	cursor := "  "
	nameFg := lipgloss.NewStyle()
	if idx == m.cursor {
		// Always mark the cursor repo so you can tell which repo the Branches/Log
		// panels belong to; highlight it only when the Repos panel is focused.
		if m.focus == panelRepos {
			cursor = styleCursor.Render("> ")
			nameFg = nameFg.Foreground(borderAccent).Bold(true)
		} else {
			cursor = styleDim.Render("> ")
		}
	}
	// name, then the current branch in dim parens (and the latest tag too when
	// showTagsInline is on), all fit within the name column.
	tag := ""
	if m.showTagsInline {
		tag = r.latestTag
	}
	name, branch, tag := fitNameSuffixes(r.repo.Name, currentBranch(r.status), tag, nameW)
	content := nameFg.Render(name)
	if branch != "" {
		content += styleDim.Render(" (" + branch + ")")
	}
	if tag != "" {
		content += styleDim.Render(" (" + tag + ")")
	}
	nameCell := lipgloss.NewStyle().Width(nameW).Render(content)
	dirtyCell := lipgloss.NewStyle().Width(wDirty).Render(dirtyBadge(r.status))
	statusCell := lipgloss.NewStyle().Width(wStatus).Render(syncGlyph(r, m.cfg.UnicodeGlyphs()))
	// Two spaces after the cursor fill the mark + gutter columns computeDims
	// budgets for, keeping the name/dirty/status columns aligned.
	return cursor + "  " + nameCell + " " + dirtyCell + " " + statusCell
}

func (m Model) renderRepoBody(d dims, height int) string {
	// Build the grouped rows tracking the cursor's line, then window to `height`
	// so the highlighted repo stays visible instead of running off the bottom.
	var lines []string
	cursorLine := 0
	lastGroup := ""
	for i, r := range m.visibleRepos() {
		if r.repo.Group != lastGroup {
			lines = append(lines, styleGroup.Render(r.repo.Group))
			lastGroup = r.repo.Group
		}
		if i == m.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, m.renderRow(i, r, d.nameW))
	}
	if height < 1 {
		height = 1
	}
	start, end := window(len(lines), cursorLine, height)
	return strings.Join(lines[start:end], "\n")
}

func (m Model) renderBranches(contentW, innerH int) string {
	vb := m.visibleBranches()
	if len(vb) == 0 && m.filterPanel == panelBranches && m.filter != "" {
		return styleDim.Render("(no branches match \"" + m.filter + "\")")
	}
	start, end := window(len(vb), m.branchCursor, innerH)
	var b strings.Builder
	for i := start; i < end; i++ {
		br := vb[i]
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
	// MaxWidth (ANSI-aware) clips long names to the panel width so they can't wrap.
	return lipgloss.NewStyle().MaxWidth(contentW).Render(b.String())
}

// tabBar renders a "[N Name] | ..." bar for a multi-view slot: the active tab in
// reverse-video, the rest dim, joined by dim "│" dividers, so every view is
// discoverable. active is the index of the current tab.
func tabBar(tabs []struct {
	n    int
	name string
}, active int) string {
	activeStyle := lipgloss.NewStyle().Reverse(true).Bold(true)
	out := make([]string, len(tabs))
	for i, v := range tabs {
		text := fmt.Sprintf(" %d %s ", v.n, v.name)
		if i == active {
			out[i] = activeStyle.Render(text)
		} else {
			out[i] = styleDim.Render(text)
		}
	}
	return strings.Join(out, styleDim.Render("│"))
}

// topTabs is the tab bar for the top-right slot: Branches (3) and PRs (4).
func (m Model) topTabs() string {
	return tabBar([]struct {
		n    int
		name string
	}{{3, "Branches"}, {4, "PRs"}}, int(m.topView))
}

// bottomTabs is the tab bar for the bottom slot: Graph (5), Changes (6), Output
// (7) — a "*" marks Output while a script runs.
func (m Model) bottomTabs() string {
	out := "Output"
	if m.outputRunning {
		out = "Output*"
	}
	return tabBar([]struct {
		n    int
		name string
	}{{5, "Graph"}, {6, "Changes"}, {7, out}}, int(m.bottomView))
}

// topHint is a short, contextual action hint appended to the top-right slot's tab
// bar while it's focused (ASCII only — it sits in the width-measured title).
func (m Model) topHint() string {
	if m.focus != panelBranches {
		return ""
	}
	var h string
	switch {
	case m.topView == tvPRs && m.ghAvailable:
		h = "enter: checkout   m: toggle list"
	case m.topView == tvBranches:
		h = "enter: checkout"
	}
	if h == "" {
		return ""
	}
	return styleDim.Render("   " + h)
}

// bottomHint is a short, contextual action hint appended to the bottom panel's
// title while it's focused, so the graph → files → diff drill-down is
// discoverable (ASCII only — it sits in the width-measured title border).
func (m Model) bottomHint() string {
	if m.focus != panelBottom {
		return ""
	}
	var h string
	switch {
	case m.bottomView == bvGraph:
		h = "enter: its files" // WIP is always drillable, even with no commits
	case m.bottomView == bvChanges && m.changeShowDiff:
		h = "esc: back"
	case m.bottomView == bvChanges && len(m.changeFiles) > 0:
		h = "enter: diff   esc: back"
	}
	if h == "" {
		return ""
	}
	return styleDim.Render("   " + h)
}

// renderTop renders the active view of the top-right slot: the highlighted repo's
// branches, or the GitHub PR list.
func (m Model) renderTop(contentW, innerH int) string {
	if m.topView == tvPRs {
		return m.renderPRsView(contentW, innerH)
	}
	return m.renderBranches(contentW, innerH)
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

// prUnavailableHint explains why the PR pane is empty when gh isn't usable —
// still probing, not installed, or installed but not signed in.
func (m Model) prUnavailableHint() string {
	switch {
	case !m.ghProbed:
		return "checking GitHub..."
	case !m.ghInstalled:
		return "gh not installed\nsee cli.github.com to enable the PRs tab"
	default:
		return "gh found but not signed in\nrun: gh auth login"
	}
}

// prEmptyState is the message (and optional secondary hint) shown when the active
// PR list has nothing to show — filtered out, still loading, errored, or empty.
func (m Model) prEmptyState() (msg, hint string) {
	switch {
	case m.filterPanel == filterPRs && m.filter != "":
		return "No PRs match \"" + m.filter + "\"", "esc to clear the filter"
	case !m.prLoaded:
		return "Loading PRs...", ""
	case m.prErr != nil:
		return "Couldn't load PRs", "is gh up to date? needs 2.12+"
	case m.prShowReview:
		return "You're all caught up", "no PRs awaiting your review  ·  m: my PRs"
	default:
		return "No open PRs authored by you", "m: review requests"
	}
}

// centerBlock centers a (possibly multi-line) string in a w×h area, so short
// empty / hint messages sit calmly in the middle of a pane instead of the corner.
func centerBlock(w, h int, s string) string {
	if h < 1 {
		h = 1
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s)
}

// renderPRsView renders the PRs sub-view: a sub-toggle header (my PRs / review
// requests, switched with `m`) pinned to the top, a blank spacer, then the active
// (filtered) PR list. Empty / loading / unavailable states are centered for a
// calmer, less-crammed look.
func (m Model) renderPRsView(contentW, innerH int) string {
	if !m.ghAvailable {
		return centerBlock(contentW, innerH, styleDim.Align(lipgloss.Center).Render(m.prUnavailableHint()))
	}

	// Sub-toggle header: the active list emphasized, the other reachable via `m`.
	my := "my PRs (" + strconv.Itoa(len(m.prMine)) + ")"
	rev := "review requests (" + strconv.Itoa(len(m.prReview)) + ")"
	sep := styleDim.Render("    ")
	var header string
	if m.prShowReview {
		header = " " + styleDim.Render("m: "+my) + sep + styleGroup.Render(rev)
	} else {
		header = " " + styleGroup.Render(my) + sep + styleDim.Render("m: "+rev)
	}

	prs := m.visiblePRs()
	if len(prs) == 0 {
		msg, hint := m.prEmptyState()
		block := styleDim.Render(msg)
		if hint != "" {
			block = lipgloss.JoinVertical(lipgloss.Center, styleDim.Render(msg), "", styleDim.Render(hint))
		}
		// Header pinned to the top; the message centered in the space below it.
		body := header + "\n" + centerBlock(contentW, max(1, innerH-1), block)
		return lipgloss.NewStyle().MaxWidth(contentW).Render(body)
	}

	listH := max(1, innerH-2) // header + a blank spacer line
	focused := m.focus == panelBranches
	start, end := window(len(prs), m.prCursor, listH)
	var b strings.Builder
	b.WriteString(header + "\n\n")
	for i := start; i < end; i++ {
		pr := prs[i]
		cur := "   "
		if focused && i == m.prCursor {
			cur = " " + styleCursor.Render("> ")
		}
		repo := pr.RepoSlug
		if s := strings.LastIndex(repo, "/"); s >= 0 {
			repo = repo[s+1:] // just the repo name; owner is usually the same org
		}
		draft := ""
		if pr.IsDraft {
			draft = styleDim.Render(" [draft]")
		}
		row := cur + styleYellow.Render("#"+strconv.Itoa(pr.Number)) + "  " +
			styleGroup.Render("@"+pr.Author) + "  " + pr.Title + draft +
			styleDim.Render("  "+repo)
		b.WriteString(row + "\n")
	}
	return lipgloss.NewStyle().MaxWidth(contentW).Render(b.String())
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
		return centerBlock(contentW, innerH, styleDim.Render(truncate("no changes in "+what, contentW)))
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

// renderOutputView shows the live combined output of the last script run, or a
// centered hint when there's nothing to show yet.
func (m Model) renderOutputView(contentW, innerH int) string {
	if len(m.outputLines) == 0 {
		msg := "run a script from [2] Scripts to see its output here"
		if m.outputRunning {
			msg = "running " + m.outputTitle + "..."
		}
		return centerBlock(contentW, innerH, styleDim.Render(truncate(msg, contentW)))
	}
	start, end := window(len(m.outputLines), m.outputOffset, innerH)
	return lipgloss.NewStyle().MaxWidth(contentW).Render(strings.Join(m.outputLines[start:end], "\n"))
}

func (m Model) renderScripts(contentW, innerH int) string {
	vs := m.visibleScripts()
	if len(vs) == 0 {
		if m.filterPanel == panelScripts && m.filter != "" {
			return styleDim.Render("(no scripts match \"" + m.filter + "\")")
		}
		return styleDim.Render("(no scripts found)")
	}
	start, end := window(len(vs), m.scriptCursor, innerH)
	var b strings.Builder
	for i := start; i < end; i++ {
		cursor := "  "
		if m.focus == panelScripts && i == m.scriptCursor {
			cursor = styleCursor.Render("> ")
		}
		b.WriteString(cursor + vs[i].Name + "\n")
	}
	return lipgloss.NewStyle().MaxWidth(contentW).Render(b.String())
}

func (m Model) footer() string {
	enter := "enter branches"
	switch m.focus {
	case panelScripts:
		enter = "enter run"
	case panelBranches:
		enter = "enter checkout"
		if m.topView == tvPRs {
			enter = "enter checkout PR"
		}
	}
	return styleDim.Render(
		enter + " | z zoom | g graph | n news | t tags | F changed | s sync | p push | d/D discard | o open | r refetch | ? keys | , settings | q quit")
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

// helpView renders the full-screen overlay: the Settings radio-list, or the
// keybindings + status reference when toggled with tab.
func (m Model) helpView() string {
	if m.showKeys {
		return m.overlayBox(m.keysBody())
	}
	return m.overlayBox(m.settingsBody())
}

// overlayBox wraps overlay content in a full-screen focused panel, centred both
// ways. The body is a block of already-aligned lines — Place moves the block as a
// unit, so the columns inside it stay aligned with each other while the block
// itself sits in the middle instead of hugging the top-left of a full-screen box.
func (m Model) overlayBox(body string) string {
	tw, th := m.width, m.height
	if tw <= 0 {
		tw = minTermW
	}
	if th <= 0 {
		th = minTermH
	}
	// panelStyle(w, h) sets the block size INCLUDING its Padding(0,1), so the
	// content area is 2 cells narrower; the border adds one more on each side.
	contentW, contentH := tw-4, th-2
	centred := lipgloss.Place(contentW, contentH, lipgloss.Center, lipgloss.Center,
		clampLines(body, contentH))
	box := panelStyle(tw-2, th-2, true).Render(centred)
	return lipgloss.NewStyle().MaxWidth(tw).Render(box)
}

// settingsBody is the Settings radio-list: a group per setting (theme, AI
// harness, glyphs, editor), each option its own row. The cursor row is
// highlighted, the selected value in each group is marked (*), and a theme row
// previews live as the cursor lands on it. Uninstalled harnesses are grayed.
func (m Model) settingsBody() string {
	radioMark := func(selected bool) string {
		if selected {
			return styleGreen.Render("(*) ")
		}
		return "( ) "
	}
	// line renders "> (mark) label", accent when it's the cursor row.
	line := func(cursor bool, mark, label string) string {
		cur := "  "
		lbl := styleDim.Render(label)
		if cursor {
			cur = styleCursor.Render("> ")
			lbl = styleCursor.Render(label)
		}
		return "   " + cur + mark + lbl
	}
	hdr := func(kind settingKind) string {
		switch kind {
		case skTheme:
			return styleGroup.Render("Theme") + styleDim.Render("   (previews live)")
		case skHarness:
			return styleGroup.Render("AI harness") + styleDim.Render("   (grey = not installed)")
		case skNewsDays:
			return styleGroup.Render("News window") + styleDim.Render("   (top-bar lookback)")
		case skMaxDepth:
			return styleGroup.Render("Scan depth") + styleDim.Render("   (rescans on select)")
		case skGlyph:
			return styleGroup.Render("Ahead / behind glyphs")
		default:
			return styleGroup.Render("Editor") + styleDim.Render("   (`o` opens the repo)")
		}
	}

	// Two columns split at skMaxDepth — one column of 26 rows scrolls at the 80x20
	// minimum, side-by-side it fits. settingRows() order is unchanged, so j/k still
	// walks top-to-bottom, left then right.
	defaultDepth := config.Default().MaxDepth // hoisted: this loop runs every render
	var left, right []string
	cursorLine := 0
	prev := settingKind(-1)
	for i, r := range settingRows() {
		col := &left
		if r.kind >= skMaxDepth {
			col = &right
		}
		if r.kind != prev {
			if len(*col) > 0 {
				*col = append(*col, "") // air above every group but the column's first
			}
			*col = append(*col, hdr(r.kind))
			prev = r.kind
		}
		cursor := m.settingsCursor == i
		if cursor {
			// JoinHorizontal aligns both columns at the top, so a row's index inside
			// its own column IS its line in the joined block.
			cursorLine = len(*col)
		}
		switch r.kind {
		case skTheme:
			*col = append(*col, line(cursor, radioMark(m.cfg.Theme == r.val), r.val))
		case skHarness:
			installed := harness.Available(r.val)
			label := r.val
			mark := radioMark(installed && m.cfg.Harness == r.val)
			if !installed {
				label += "  (not installed)"
			}
			cur := "  " // grayed harnesses stay dim even under the cursor
			if cursor {
				cur = styleCursor.Render("> ")
			}
			lbl := styleDim.Render(label)
			if cursor && installed {
				lbl = styleCursor.Render(label)
			}
			*col = append(*col, "   "+cur+mark+lbl)
		case skNewsDays:
			d, _ := strconv.Atoi(r.val)
			label := r.val + " days"
			if d == 1 {
				label = "1 day"
			}
			*col = append(*col, line(cursor, radioMark(m.cfg.NewsDays == d), label))
		case skMaxDepth:
			d, _ := strconv.Atoi(r.val)
			label := r.val + " levels"
			if d == 1 {
				label = "1 level"
			}
			if d == defaultDepth {
				label += "  (default)"
			}
			*col = append(*col, line(cursor, radioMark(m.cfg.MaxDepth == d), label))
		case skGlyph:
			label := "unicode  (arrows)"
			if r.val == "ascii" {
				label = "ascii    (+ / -)"
			}
			sel := (r.val == "unicode") == m.cfg.UnicodeGlyphs()
			*col = append(*col, line(cursor, radioMark(sel), label))
		case skEditor:
			val := m.cfg.OpenCmd
			hint := ""
			if m.editingOpenCmd {
				val = m.openCmdBuf + "_"
				hint = "   enter saves · esc cancels"
			} else if cursor {
				hint = "   enter to edit"
			}
			ecur := "  "
			eval := styleDim.Render(val)
			if cursor {
				ecur = styleCursor.Render("> ")
				eval = styleCursor.Render(val)
			}
			*col = append(*col, "   "+ecur+eval+styleDim.Render(hint))
		}
	}
	// Window both columns together (they share start/end since JoinHorizontal
	// aligns them at the top). A no-op at every supported size; only bites if the
	// list grows.
	th := m.height
	if th <= 0 {
		th = minTermH
	}
	avail := (th - 2) - 4 // box inner height minus title, blank, blank, footer
	if avail < 1 {
		avail = 1
	}
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	for len(left) < n {
		left = append(left, "")
	}
	for len(right) < n {
		right = append(right, "")
	}
	start, end := window(n, cursorLine, avail)
	left, right = left[start:end], right[start:end]

	// Pad the left column to a fixed width (ANSI-aware) so the right starts straight.
	const colGap = 4
	leftW := 0
	for _, ln := range left {
		if w := lipgloss.Width(ln); w > leftW {
			leftW = w
		}
	}
	for i, ln := range left {
		left[i] = ln + strings.Repeat(" ", max(0, leftW+colGap-lipgloss.Width(ln)))
	}
	cols := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(left, "\n"), strings.Join(right, "\n"))

	body := []string{styleTitle.Render("manygit — settings") + styleDim.Render("   (,)"), ""}
	body = append(body, strings.Split(cols, "\n")...)
	body = append(body, "", styleDim.Render("j/k move · enter select · tab keys · esc close"))
	// Left-join first so it pads every line to the block width — the title and
	// footer then share a straight left edge with the columns instead of overlayBox
	// centring each on its own.
	return lipgloss.JoinVertical(lipgloss.Left, body...)
}

// keysBody is the two-column keybinding + status-legend reference (two columns so
// it fits shorter terminals).
func (m Model) keysBody() string {
	kr := func(key, desc string) string {
		// Width(10), not 8: lipgloss's Width HARD-WRAPS rather than overflowing, so
		// any key wider than the column breaks across two lines mid-word. The
		// longest keys here are exactly 10 cells (left/right) and 9 (no-remote,
		// shift+tab). At the documented 80-col minimum this leaves the description
		// 24 cells, which MaxWidth clips cleanly.
		return "  " + lipgloss.NewStyle().Width(10).Render(key) + styleDim.Render(desc)
	}
	up, down := "+", "-"
	if m.cfg.UnicodeGlyphs() {
		up, down = "↑", "↓"
	}
	head := []string{
		styleTitle.Render("manygit — keybindings") + styleDim.Render("   (tab or , for settings · esc close)"),
		"",
	}
	left := []string{
		styleGroup.Render("Panels & navigation"),
		kr("1/2/3", "focus Repos / Scripts / Branches"),
		kr("4", "PRs (beside Branches)"),
		kr("5/6/7", "bottom: Graph / Changes / Output"),
		kr("tab", "cycle panels"),
		kr("shift+tab", "cycle panels backwards"),
		kr("[ ]", "cycle the focused pane's tabs"),
		kr("z", "zoom the focused pane full-screen"),
		kr("j/k", "move in the focused panel"),
		kr("left/right", "hop between Repos and Branches"),
		kr("enter", "branches / checkout / run / checkout PR"),
		kr("g", "full-screen commit graph"),
		kr("n", "full-screen news feed (all headlines)"),
		kr("t", "toggle each repo's latest tag inline"),
		kr("F", "only changed / unsynced repos"),
		kr("/", "filter the focused list"),
		kr("esc", "back out one layer of state"),
		"",
		styleGroup.Render("GitHub PRs (4)") + styleDim.Render("   (needs gh)"),
		kr("m", "toggle mine / review-requested"),
		kr("enter", "checkout the PR's branch in its repo"),
		"",
		styleGroup.Render("Graph (5) -> Changes (6)"),
		kr("5 j/k", "select a commit (WIP on top)"),
		kr("5 enter", "show its changed files"),
		kr("6 j/k", "pick a file"),
		kr("6 enter", "view its diff"),
		kr("esc", "back: diff / files / graph"),
	}
	right := []string{
		styleGroup.Render("Actions") + styleDim.Render(" on the > repo"),
		kr("s", "sync (fetch + pull --ff-only)"),
		kr("p", "push"),
		kr("f/r", "fetch current / refetch all"),
		kr("b/enter", "checkout selected branch"),
		kr("d/D", "discard changes / +untracked (confirm)"),
		kr("o", "open the repo in your editor"),
		"",
		styleGroup.Render("This screen"),
		kr("?", "this page"),
		kr(",", "settings (themes, harness, depth)"),
		kr("tab", "flip keys <-> settings"),
		kr("q", "quit manygit"),
		"",
		styleGroup.Render("Status column"),
		kr(styleGreen.Render("ok"), "up to date with upstream"),
		kr(styleYellow.Render(up+"N"), "ahead — commits to PUSH"),
		kr(styleCyan.Render(down+"N"), "behind — commits to PULL"),
		kr(styleMagenta.Render(up+"N"+down+"M"), "diverged"),
		kr(styleOrange.Render("*N"), "N files changed (dirty)"),
		kr(styleDim.Render("~ ."), "fetching / loading"),
		kr(styleDim.Render("no-remote"), "local-only repo (no remote configured)"),
		kr(styleRed.Render("!"), "branch has no upstream, or error"),
	}
	tw := m.width
	if tw <= 0 {
		tw = minTermW
	}
	// Clip each column to its width budget (ANSI-aware) so neither can wrap and
	// break alignment — leftW + rightW == the overlay's inner content width, so
	// the joined row never exceeds it (which panelStyle would otherwise wrap).
	// Left content is clipped to leftW-gutter then padded to leftW, so there's
	// always a gap before the right column even when a left line is truncated.
	const gutter = 2
	leftW := (tw - 4) / 2
	rightW := (tw - 4) - leftW
	leftBlock := make([]string, len(left))
	for i, ln := range left {
		c := lipgloss.NewStyle().MaxWidth(leftW - gutter).Render(ln)
		leftBlock[i] = c + strings.Repeat(" ", max(0, leftW-lipgloss.Width(c)))
	}
	rightBlock := make([]string, len(right))
	for i, ln := range right {
		rightBlock[i] = lipgloss.NewStyle().MaxWidth(rightW).Render(ln)
	}
	cols := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(leftBlock, "\n"), strings.Join(rightBlock, "\n"))
	return strings.Join(head, "\n") + "\n" + cols
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

// newsView renders the full-screen news feed — every headline at once (the top
// bar only rotates through one at a time), with j/k scrolling.
func (m Model) newsView() string {
	tw, th := m.width, m.height
	if tw <= 0 {
		tw = minTermW
	}
	if th <= 0 {
		th = minTermH
	}
	innerH := th - 2 // border rows
	if innerH < 3 {
		innerH = 3
	}
	var content string
	switch {
	case len(m.newsFeed) == 0 && m.newsLoading:
		content = styleDim.Render("(summarizing recent commits…)")
	case len(m.newsFeed) == 0:
		content = styleDim.Render(fmt.Sprintf("(no main-branch activity in the last %d days, or no AI harness set)", m.cfg.NewsDays))
	default:
		lines := make([]string, len(m.newsFeed))
		for i, h := range m.newsFeed {
			lines[i] = styleDim.Render(fmt.Sprintf("%2d ", i+1)) + h
		}
		start := m.newsOffset
		if start > len(lines)-1 {
			start = len(lines) - 1
		}
		if start < 0 {
			start = 0
		}
		end := start + innerH
		if end > len(lines) {
			end = len(lines)
		}
		content = lipgloss.NewStyle().MaxWidth(tw - 4).Render(strings.Join(lines[start:end], "\n"))
	}
	title := fmt.Sprintf("News — %d headlines  (j/k scroll, esc close)", len(m.newsFeed))
	box := titledBox(title, tw-2, innerH, true, content)
	return lipgloss.NewStyle().MaxWidth(tw).Render(box)
}

// changelogView is the one-time post-update screen: the release notes since the
// version the user updated from, newest first, scrollable with j/k. Each line was
// tagged by changelogLines with a kind prefix (clHead/clBody/clMark) so we colour
// it without re-parsing the markdown.
func (m Model) changelogView() string {
	tw, th := m.width, m.height
	if tw <= 0 {
		tw = minTermW
	}
	if th <= 0 {
		th = minTermH
	}
	innerH := th - 2 // rows inside the border

	// A centred column, not edge-to-edge: cap at a readable measure, indent, then
	// Place the block in the middle. Blank line before each heading (bar the first)
	// so versions don't run together.
	const measure = 64 // readable line length; the panel is usually wider
	colW := tw - 8
	if colW > measure {
		colW = measure
	}
	if colW < 20 {
		colW = 20
	}
	clip := lipgloss.NewStyle().MaxWidth(colW)
	indent := "  "

	newBadge := lipgloss.NewStyle().Reverse(true).Bold(true).Render(" NEW ")
	styled := make([]string, 0, len(m.changelog))
	for i, ln := range m.changelog {
		switch {
		case strings.HasPrefix(ln, clHeadNew):
			if i > 0 {
				styled = append(styled, "")
			}
			h := styleTitle.Render(strings.TrimPrefix(ln, clHeadNew))
			styled = append(styled, clip.Render(h)+"  "+newBadge)
		case strings.HasPrefix(ln, clHead):
			if i > 0 {
				styled = append(styled, "")
			}
			styled = append(styled, clip.Render(styleTitle.Render(strings.TrimPrefix(ln, clHead))))
		case strings.HasPrefix(ln, clMark):
			styled = append(styled, indent+clip.Render(styleGreen.Render(strings.TrimPrefix(ln, clMark))))
		case strings.HasPrefix(ln, clBody):
			body := strings.TrimPrefix(ln, clBody)
			if strings.TrimSpace(body) == "" {
				styled = append(styled, "")
			} else {
				styled = append(styled, indent+clip.Render(styleDim.Render(body)))
			}
		default:
			styled = append(styled, indent+clip.Render(styleDim.Render(ln)))
		}
	}

	start := clampInt(m.changelogOffset, 0, max(0, len(styled)-1))
	end := start + innerH
	if end > len(styled) {
		end = len(styled)
	}
	window := styled[start:end]

	// Fixed block width so Place moves the column as one unit (else each line
	// centres on its own and the left edge zig-zags).
	blockW := 0
	for _, ln := range window {
		if w := lipgloss.Width(ln); w > blockW {
			blockW = w
		}
	}
	for i, ln := range window {
		window[i] = ln + strings.Repeat(" ", max(0, blockW-lipgloss.Width(ln)))
	}
	block := strings.Join(window, "\n")

	// Centred horizontally; centred vertically when short, top-aligned once it scrolls.
	vAlign := lipgloss.Center
	if len(window) >= innerH {
		vAlign = lipgloss.Top
	}
	content := lipgloss.Place(tw-4, innerH, lipgloss.Center, vAlign, block)

	more := ""
	if end < len(styled) {
		more = "  ↓ more"
	}
	title := "What's new" + more + "  (j/k scroll · esc continue)"
	box := titledBox(title, tw-2, innerH, true, content)
	return lipgloss.NewStyle().MaxWidth(tw).Render(box)
}

func (m Model) View() string {
	if m.showChangelog {
		return m.changelogView() // the one-time post-update splash sits above all
	}
	if m.showGraph {
		return m.graphView()
	}
	if m.showNews {
		return m.newsView()
	}
	if m.showHelp {
		return m.helpView()
	}
	if m.zoomed {
		return m.zoomedView()
	}
	d := computeDims(m.width, m.height, m.showTagsInline)
	tw := m.width
	if tw <= 0 {
		tw = minTermW
	}
	brand := styleTitle.Render("manygit") + "  "
	title := brand + m.topBar(max(0, tw-lipgloss.Width(brand)))

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
		lipgloss.NewStyle().MaxWidth(d.leftW-2).Render(clampLines(m.renderRepoBody(d, reposInner), reposInner)))
	scriptsPanel := titledPanel(2, "Scripts", d.leftW, scriptsInner, m.focus == panelScripts,
		clampLines(m.renderScripts(d.leftW-2, scriptsInner), scriptsInner))
	left := lipgloss.JoinVertical(lipgloss.Left, reposPanel, scriptsPanel)

	// right column: two stacked multi-view slots sharing the left panel's total
	// height. Top = Branches (3) / PRs (4); bottom = Graph (5) / Changes (6) /
	// Output (7). Each shows a tab bar so the other views are discoverable.
	topInner := max((d.bodyH-2)*40/100, 3)
	botInner := max((d.bodyH-2)-topInner, 3)
	top := titledBarBox(m.topTabs()+m.topHint(), d.rightW, topInner, m.focus == panelBranches,
		clampLines(m.renderTop(d.rightW-2, topInner), topInner))
	bottom := titledBarBox(m.bottomTabs()+m.bottomHint(), d.rightW, botInner, m.focus == panelBottom,
		clampLines(m.renderBottom(d.rightW-2, botInner), botInner))
	right := lipgloss.JoinVertical(lipgloss.Left, top, bottom)

	cols := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gutter), right)
	view := lipgloss.JoinVertical(lipgloss.Left, title, "", cols, m.bottomBar(tw))
	return lipgloss.NewStyle().MaxWidth(tw).Render(view)
}

// zoomedView renders just the focused pane, maximized to the whole screen (z).
// Zoom follows focus, so switching panes (1..6) shows the new one full-screen.
func (m Model) zoomedView() string {
	tw, th := m.width, m.height
	if tw <= 0 {
		tw = minTermW
	}
	if th <= 0 {
		th = minTermH
	}
	title := styleTitle.Render("manygit") + "  " +
		styleDim.Render(fmt.Sprintf("%d repos", len(m.repos))) +
		styleDim.Render("   [zoom — z to restore]")

	innerW := tw - 2                                   // panel inner (content + padding)
	innerH := th - headerRows - footerRows - borderPad // panel inner height
	if innerH < 3 {
		innerH = 3
	}
	contentW := innerW - 2 // content area (minus panel padding)

	var panel string
	switch m.focus {
	case panelBottom:
		panel = titledBarBox(m.bottomTabs()+m.bottomHint(), innerW, innerH, true,
			clampLines(m.renderBottom(contentW, innerH), innerH))
	case panelScripts:
		panel = titledPanel(2, "Scripts", innerW, innerH, true,
			clampLines(m.renderScripts(contentW, innerH), innerH))
	case panelBranches:
		panel = titledBarBox(m.topTabs()+m.topHint(), innerW, innerH, true,
			clampLines(m.renderTop(contentW, innerH), innerH))
	default: // panelRepos
		zd := dims{leftW: innerW, rightW: innerW, bodyH: innerH, nameW: max(8, contentW-20)}
		panel = titledPanel(1, "Repos", innerW, innerH, true,
			clampLines(m.renderRepoBody(zd, innerH), innerH))
	}
	view := lipgloss.JoinVertical(lipgloss.Left, title, "", panel, m.bottomBar(tw))
	return lipgloss.NewStyle().MaxWidth(tw).Render(view)
}

// topBar fills the space after the brand: the news/count content on the left and
// a flush-right PR badge (when gh is available and something is pending).
func (m Model) topBar(width int) string {
	badge := m.prBadge()
	bw := lipgloss.Width(badge)
	if bw == 0 || bw+2 >= width {
		return m.topBarMain(width) // no badge (or no room) — main fills the width
	}
	main := m.topBarMain(width - bw - 2) // reserve the badge + a 1-cell gap
	gap := width - lipgloss.Width(main) - bw
	if gap < 1 {
		gap = 1
	}
	return main + strings.Repeat(" ", gap) + badge
}

// prBadge is the compact right-aligned PR summary for the top bar: the count of
// PRs awaiting your review (actionable, colored) and your own open PRs (dim).
// Shown only when gh is available and at least one is nonzero. ASCII, so it can't
// drift columns.
func (m Model) prBadge() string {
	if !m.ghAvailable {
		return ""
	}
	rev, mine := len(m.prReview), len(m.prMine)
	if rev == 0 && mine == 0 {
		return ""
	}
	var parts []string
	if rev > 0 {
		parts = append(parts, styleYellow.Render("review "+strconv.Itoa(rev)))
	}
	if mine > 0 {
		parts = append(parts, styleDim.Render("mine "+strconv.Itoa(mine)))
	}
	return styleGroup.Render("PR ") + strings.Join(parts, "  ")
}

// topBarMain fills the space after the brand: the AI news feed (rotating commit
// headlines) when available, otherwise the repo count / filter context.
func (m Model) topBarMain(width int) string {
	// While filtering or in the attention view, show the count context there.
	if m.filtering || m.filter != "" || m.filterAttention {
		count := fmt.Sprintf("%d of %d repos", len(m.visibleRepos()), len(m.repos))
		if m.filterAttention {
			count += "  " + styleYellow.Render("[changed / unsynced]")
		}
		return lipgloss.NewStyle().MaxWidth(width).Render(styleDim.Render(count))
	}
	if len(m.newsFeed) == 0 {
		count := fmt.Sprintf("%d repos", len(m.repos))
		if m.newsLoading {
			count += styleDim.Render("   summarizing commits...")
		}
		return lipgloss.NewStyle().MaxWidth(width).Render(styleDim.Render(count))
	}
	headline := m.newsFeed[m.newsIndex%len(m.newsFeed)]
	line := styleGroup.Render("news ") + headline
	if len(m.newsFeed) > 1 {
		line += styleDim.Render(fmt.Sprintf("   (%d/%d)", m.newsIndex%len(m.newsFeed)+1, len(m.newsFeed)))
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

// bottomBar is the footer line: the status/filter/key-hints on the left and the
// GitHub + AI-harness indicators on the right. The right cluster is always shown;
// the left side is clipped to make room for it.
func (m Model) bottomBar(width int) string {
	right := m.rightIndicators()
	rw := lipgloss.Width(right)
	if rw+1 >= width {
		return right // extremely narrow: just the harness
	}
	left := lipgloss.NewStyle().MaxWidth(width - rw - 1).Render(m.statusOrFilterLine())
	gap := width - lipgloss.Width(left) - rw
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// rightIndicators is the bottom-bar right cluster: the GitHub login (only when
// gh is available+authed) then the AI harness (always shown).
func (m Model) rightIndicators() string {
	h := m.harnessIndicator()
	if g := m.githubIndicator(); g != "" {
		return g + "   " + h
	}
	return h
}

// githubIndicator shows "github: <user>" when gh is available and authenticated,
// else "" (users without gh see just the harness). Mirrors harnessIndicator.
func (m Model) githubIndicator() string {
	if !m.ghAvailable || m.ghUser == "" {
		return ""
	}
	return styleGroup.Render("github: " + m.ghUser)
}

// harnessIndicator shows the active AI harness (dim/`no harness` if none is
// selected or the selected one isn't installed).
func (m Model) harnessIndicator() string {
	if m.cfg.Harness == "" {
		return styleDim.Render("no AI harness")
	}
	label := "harness: " + m.cfg.Harness
	if harness.Available(m.cfg.Harness) {
		return styleGroup.Render(label)
	}
	return styleDim.Render(label + " (n/a)")
}
