package tui

import (
	"strconv"

	"manygit/internal/harness"
)

// The ? settings screen is a flat radio list. Each selectable row is a
// settingRow; settingsCursor indexes into settingRows().
type settingKind int

const (
	skTheme settingKind = iota
	skHarness
	skNewsDays
	skGlyph
	skEditor
)

// newsDayOptions are the selectable top-bar news-feed windows, in days.
var newsDayOptions = []int{1, 3, 7, 14}

type settingRow struct {
	kind settingKind
	val  string // theme/harness/glyph value; "" for the editor row
}

// settingRows is the ordered list of selectable rows: every theme, every known
// harness, the news-window options, the two glyph modes, then the editor.
func settingRows() []settingRow {
	rows := make([]settingRow, 0, len(themeList)+len(harness.All)+len(newsDayOptions)+3)
	for _, t := range themeList {
		rows = append(rows, settingRow{skTheme, t.Name})
	}
	for _, h := range harness.All {
		rows = append(rows, settingRow{skHarness, h.Name})
	}
	for _, d := range newsDayOptions {
		rows = append(rows, settingRow{skNewsDays, strconv.Itoa(d)})
	}
	rows = append(rows, settingRow{skGlyph, "unicode"}, settingRow{skGlyph, "ascii"})
	rows = append(rows, settingRow{skEditor, ""})
	return rows
}

func settingsItemCount() int { return len(settingRows()) }

// settingRowIndex returns the index of the first row matching kind (and val,
// when non-empty), or -1. Handy for jumping the cursor and for tests.
func settingRowIndex(kind settingKind, val string) int {
	for i, r := range settingRows() {
		if r.kind == kind && (val == "" || r.val == val) {
			return i
		}
	}
	return -1
}
