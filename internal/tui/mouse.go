package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The mouse is a second way to do what the keys already do — never a way to do
// more. A click focuses a pane, moves its cursor, or switches its tab; the wheel
// is j/k for the pane under the pointer. Nothing that touches a repo (checkout,
// sync, push, discard, run) is reachable by mouse, because a stray click is far
// easier than a stray keypress. Double-click is deliberately not "enter".
//
// Everything here reads the same geometry the view draws with — mainLayout,
// zoomInner, window(), repoLines, the tab lists — so a click lands on the row
// that was drawn under it and not on one computed a second, driftable way.

// paneHit is where a mouse event landed, in terms of a pane.
type paneHit struct {
	panel panel
	// row is the content line under the pointer (0 = the first line inside the
	// border), or -1 on the top border, where the tab bar sits.
	row int
	// col is the offset from where the border's label starts, for tab hits.
	col int
	// inner is the pane's inner height — the h the renderer windowed with.
	inner int
}

// hitTest resolves a screen cell to a pane, or ok=false for the header, the
// footer, the gutter, and the bottom borders.
func (m Model) hitTest(x, y int) (paneHit, bool) {
	if m.zoomed {
		innerW, innerH := m.zoomInner()
		return boxHit(m.focus, x, y, 0, headerRows, innerW, innerH)
	}
	ly := m.mainLayout()
	d := ly.d
	leftX, rightX := 0, d.leftW+borderPad+gutter
	if x < rightX {
		if h, ok := boxHit(panelRepos, x, y, leftX, headerRows, d.leftW, ly.reposInner); ok {
			return h, true
		}
		return boxHit(panelScripts, x, y, leftX, headerRows+ly.reposInner+borderPad, d.leftW, ly.scriptsInner)
	}
	if h, ok := boxHit(panelBranches, x, y, rightX, headerRows, d.rightW, ly.topInner); ok {
		return h, true
	}
	return boxHit(panelBottom, x, y, rightX, headerRows+ly.topInner+borderPad, d.rightW, ly.botInner)
}

// boxHit tests one bordered box whose top-left corner is (x0, y0) and whose inner
// size is innerW x innerH. The label starts after "╭─", two cells in.
func boxHit(p panel, x, y, x0, y0, innerW, innerH int) (paneHit, bool) {
	if x < x0 || x >= x0+innerW+borderPad || y < y0 || y > y0+innerH {
		return paneHit{}, false // the bottom border (y0+innerH+1) is not a target
	}
	return paneHit{panel: p, row: y - y0 - 1, col: x - x0 - 2, inner: innerH}, true
}

// tabAt returns which chip of a tab bar sits at column off, or -1 (a divider, the
// hint, or past the end). Chips are joined by a one-cell "│".
func tabAt(tabs []tabDef, off int) int {
	x := 0
	for i, t := range tabs {
		w := lipgloss.Width(t.text())
		if off >= x && off < x+w {
			return i
		}
		x += w + 1
	}
	return -1
}

// mouseBlocked reports the states where a pointer event must do nothing: typing
// into a prompt or filter, and the y/N confirms. Those confirms treat ANY key as
// "no", but a click is not a key — letting one through would either cancel what
// you were reading or, worse, be mistaken for an answer.
func (m Model) mouseBlocked() bool {
	return m.filtering || m.shellPrompting || m.aiPrompting ||
		m.confirmPlan || m.confirmDiscard || m.editingOpenCmd
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.cfg.MouseEnabled() || m.mouseBlocked() {
		return m, nil
	}
	wheel := 0
	switch {
	case msg.Button == tea.MouseButtonWheelDown:
		wheel = 1
	case msg.Button == tea.MouseButtonWheelUp:
		wheel = -1
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
	default:
		return m, nil // release, motion, right/middle: nothing to do
	}
	step := tea.KeyMsg{Type: tea.KeyDown}
	if wheel < 0 {
		step = tea.KeyMsg{Type: tea.KeyUp}
	}

	// Full-screen overlays: the wheel scrolls them exactly as j/k does; clicks
	// are ignored — there is nothing in them to point at.
	if m.showChangelog || m.showGraph || m.showNews || m.showHelp {
		// On the settings face j/k moves the radio cursor and previews themes
		// live — wheeling past the theme list would flash the whole app through
		// them, so only the keys face (which j/k scrolls) takes the wheel.
		if wheel == 0 || (m.showHelp && !m.showKeys) {
			return m, nil
		}
		return m.handleKey(step)
	}

	h, ok := m.hitTest(msg.X, msg.Y)
	if !ok {
		return m, nil
	}
	m.focus = h.panel // what `tab` does: focus only, no view change
	if wheel != 0 {
		return m.handleKey(step)
	}
	if h.row < 0 {
		return m, m.clickTab(h)
	}
	return m, m.clickRow(h)
}

// clickTab switches the slot's view when the click is on one of its chips —
// through setTopView/setBottomView, the same door the number keys use.
func (m *Model) clickTab(h paneHit) tea.Cmd {
	switch h.panel {
	case panelBranches:
		if i := tabAt(m.topTabList(), h.col); i >= 0 {
			m.setTopView(topView(i))
		}
	case panelBottom:
		if i := tabAt(m.bottomTabList(), h.col); i >= 0 {
			return m.setBottomView(bottomView(i))
		}
	}
	return nil
}

// clickRow puts the pane's cursor on the line that was clicked. Each case
// re-derives its window() exactly as its renderer does; a click on a line that
// is not a row (a group header, a PR's gap, a graph connector, empty space)
// only focuses the pane.
func (m *Model) clickRow(h paneHit) tea.Cmd {
	switch h.panel {
	case panelRepos:
		vis := m.visibleRepos()
		lines, cursorLine := repoLines(vis, m.cursor)
		start, end := window(len(lines), cursorLine, max(1, h.inner))
		i := start + h.row
		if i >= end || lines[i].header || lines[i].repo == m.cursor {
			return nil
		}
		m.cursor = lines[i].repo
		m.clearBranchFilter() // same as j/k: the branch filter belonged to the old repo
		return m.contextCmd()
	case panelScripts:
		vs := m.visibleScripts()
		start, end := window(len(vs), m.scriptCursor, h.inner)
		if i := start + h.row; i < end {
			m.scriptCursor = i // select only — running a script stays on enter
		}
	case panelBranches:
		if m.topView == tvPRs {
			m.clickPR(h)
			return nil
		}
		vb := m.visibleBranches()
		start, end := window(len(vb), m.branchCursor, h.inner)
		if i := start + h.row; i < end {
			m.branchCursor = i // select only — checkout stays on enter
		}
	case panelBottom:
		m.clickBottom(h)
	}
	return nil
}

// clickPR mirrors renderPRsView: a header line and a spacer, then rows of
// prRowLines lines separated by prRowGap blanks.
func (m *Model) clickPR(h paneHit) {
	prs := m.visiblePRs()
	if !m.ghAvailable || len(prs) == 0 || h.row < 2 {
		return
	}
	start, end := window(len(prs), m.prCursor, prRowsThatFit(max(1, h.inner-2)))
	r := h.row - 2
	if r%(prRowLines+prRowGap) >= prRowLines {
		return // the blank between two PRs
	}
	if i := start + r/(prRowLines+prRowGap); i < end {
		m.prCursor = i
	}
}

// clickBottom selects in the Graph and Changes views. The diff and Output are
// read, not picked from, so a click there only focuses (the wheel scrolls them).
func (m *Model) clickBottom(h paneHit) {
	switch m.bottomView {
	case bvGraph:
		// renderGraphView's lines are [WIP, graph lines...]; only WIP and the lines
		// that carry a commit are selectable — the rest are connector art.
		selIdx := 0
		if m.graphSel >= 1 && m.graphSel-1 < len(m.graphCommits) {
			selIdx = m.graphCommits[m.graphSel-1].Line + 1
		}
		n := 1 + len(m.graphLines)
		start, end := window(n, selIdx, h.inner)
		i := start + h.row
		if i >= end {
			return
		}
		if i == 0 {
			m.graphSel = 0
			return
		}
		for k, c := range m.graphCommits {
			if c.Line+1 == i {
				m.graphSel = k + 1
				return
			}
		}
	case bvChanges:
		if m.changeShowDiff || len(m.changeFiles) == 0 {
			return
		}
		start, end := window(len(m.changeFiles), m.changeCursor, h.inner)
		if i := start + h.row; i < end {
			m.changeCursor = i // select only — the diff stays on enter
		}
	}
}
