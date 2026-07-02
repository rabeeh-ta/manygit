package tui

import "manygit/internal/harness"

// The ? settings screen is a flat radio list. Each selectable row is a
// settingRow; settingsCursor indexes into settingRows().
type settingKind int

const (
	skTheme settingKind = iota
	skHarness
	skGlyph
	skEditor
)

type settingRow struct {
	kind settingKind
	val  string // theme/harness/glyph value; "" for the editor row
}

// settingRows is the ordered list of selectable rows: every theme, every known
// harness, the two glyph modes, then the editor.
func settingRows() []settingRow {
	rows := make([]settingRow, 0, len(themeList)+len(harness.All)+3)
	for _, t := range themeList {
		rows = append(rows, settingRow{skTheme, t.Name})
	}
	for _, h := range harness.All {
		rows = append(rows, settingRow{skHarness, h.Name})
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
