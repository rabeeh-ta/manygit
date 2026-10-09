package tui

// dims are the computed panel sizes for the current terminal size. All widths
// are INNER (content+padding, excluding the 1-cell border on each side).
type dims struct {
	leftW  int // repo panel inner width
	rightW int // right-column panels inner width
	bodyH  int // panel inner height
	nameW  int // width budget for the repo-name column
}

const (
	minTermW   = 80
	minTermH   = 20
	gutter     = 1 // blank column between the two panels
	borderPad  = 2 // cells a border adds around a panel (1 each side)
	headerRows = 2 // title + blank
	footerRows = 1 // status/filter line
)

// computeDims splits the terminal so (leftW+2) + gutter + (rightW+2) == width.
// wideNames gives the Repos column a bigger share so the inline latest-tag (t)
// has room after the branch.
func computeDims(width, height int, wideNames bool) dims {
	if width < minTermW {
		width = minTermW
	}
	if height < minTermH {
		height = minTermH
	}
	usable := width - gutter - 2*borderPad
	leftPct := 38
	if wideNames {
		leftPct = 50
	}
	leftW := usable * leftPct / 100
	if leftW < 30 {
		leftW = 30
	}
	rightW := usable - leftW
	if rightW < 24 {
		rightW = 24
	}
	bodyH := height - headerRows - footerRows - borderPad
	if bodyH < 3 {
		bodyH = 3
	}
	// name column = inner width minus padding(2), cursor(2), mark(1),
	// three single-space gutters (3), dirty(wDirty), status(wStatus).
	nameW := leftW - 2 - 2 - 1 - 3 - wDirty - wStatus
	if nameW < 8 {
		nameW = 8
	}
	return dims{leftW: leftW, rightW: rightW, bodyH: bodyH, nameW: nameW}
}

// mainLayout is the four-pane view's geometry: the column dims plus each pane's
// inner height. View draws from it and the mouse hit-test reads it, so a click
// resolves against exactly the boxes on screen.
type mainLayout struct {
	d                        dims
	reposInner, scriptsInner int // left column: Repos over Scripts
	topInner, botInner       int // right column: Branches/PRs over Graph/Changes/Output
}

func (m Model) mainLayout() mainLayout {
	d := computeDims(m.width, m.height, m.showTagsInline)
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
	// right column: two stacked multi-view slots sharing the left panel's total
	// height. Top = Branches (3) / PRs (4); bottom = Graph (5) / Changes (6) /
	// Output (7). Each shows a tab bar so the other views are discoverable.
	topInner := max((d.bodyH-2)*40/100, 3)
	botInner := max((d.bodyH-2)-topInner, 3)
	return mainLayout{d: d, reposInner: reposInner, scriptsInner: scriptsInner, topInner: topInner, botInner: botInner}
}

// zoomInner is the zoomed pane's inner size (z): the whole terminal less the
// header, the footer and its own border.
func (m Model) zoomInner() (innerW, innerH int) {
	tw, th := m.width, m.height
	if tw <= 0 {
		tw = minTermW
	}
	if th <= 0 {
		th = minTermH
	}
	return tw - 2, max(th-headerRows-footerRows-borderPad, 3)
}
