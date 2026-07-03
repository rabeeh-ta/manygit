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
