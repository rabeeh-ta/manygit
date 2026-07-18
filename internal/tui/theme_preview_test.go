package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Moving the cursor on the settings page applies themes LIVE (previewSettings),
// so every way of closing the overlay owes the same cleanup: put the committed
// theme back. `,` and `esc` both do it. `?` is also a close — from the keys page
// it dismisses the overlay entirely — and used not to, so `, j ? ?` left the UI
// wearing a theme the config never committed to.
func TestTUI_ClosingHelpWithQuestionMarkDropsThemePreview(t *testing.T) {
	cfg, repos := twoRepos(t)
	cfg.Theme = "serika_dark"
	applyTheme(themeByName(cfg.Theme))

	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	committed := themeByName("serika_dark").Accent

	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	step := func(m Model, k string) Model { mm, _ := m.Update(rk(k)); return mm.(Model) }

	m = step(m, ",") // settings page, cursor on the committed theme
	if !m.showHelp || m.showKeys {
		t.Fatal(", should open the settings page")
	}

	m = step(m, "j") // preview the next theme live — NOT committed
	if borderAccent == committed {
		t.Fatal("j should have previewed a different theme")
	}
	previewed := borderAccent

	m = step(m, "?") // -> keys page, preview still live
	if !m.showKeys {
		t.Fatal("? on the settings page should switch to the keys page")
	}
	if borderAccent != previewed {
		t.Fatal("switching pages should not itself restore the theme")
	}

	m = step(m, "?") // -> close
	if m.showHelp {
		t.Fatal("? on the keys page should close the overlay")
	}
	if m.cfg.Theme != "serika_dark" {
		t.Fatalf("the theme was never committed, cfg.Theme = %q", m.cfg.Theme)
	}
	if borderAccent != committed {
		t.Errorf("closing with ? leaked the live preview: accent is %v, want the committed %v",
			borderAccent, committed)
	}
}
