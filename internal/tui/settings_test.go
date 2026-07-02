package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestMain points XDG_CONFIG_HOME at a throwaway dir for the whole package so no
// test that exercises the settings screen can ever write the developer's real
// ~/.config/manygit/config.yml.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "manygit-test-xdg")
	if err == nil {
		os.Setenv("XDG_CONFIG_HOME", dir)
	}
	code := m.Run()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

// Both overlay views (settings + keybindings) must fit within the terminal at
// every size down to the documented minimum (80x20) — no line wider than the
// terminal, no more lines than its height — or panelStyle would wrap and break
// the layout.
func TestTUI_HelpFitsTerminal(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	for _, d := range []struct{ w, h int }{{80, 20}, {100, 30}, {120, 40}} {
		cfg, repos := twoRepos(t)
		m := loadAll(t, New(cfg, repos, nil), d.w, d.h)
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
		m = mm.(Model)
		for _, view := range []string{"settings", "keys"} {
			rendered := m.helpView()
			lines := strings.Split(rendered, "\n")
			if len(lines) > d.h {
				t.Errorf("%dx%d %s: %d lines exceeds height %d (wrapped)", d.w, d.h, view, len(lines), d.h)
			}
			for _, ln := range lines {
				if w := lipgloss.Width(ln); w > d.w {
					t.Errorf("%dx%d %s: line width %d exceeds terminal %d", d.w, d.h, view, w, d.w)
				}
			}
			// the footer (with the close hint) must survive the height clamp
			if !strings.Contains(stripANSI(rendered), "esc close") {
				t.Errorf("%dx%d %s: footer 'esc close' was clipped", d.w, d.h, view)
			}
			mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // flip to keys for the 2nd pass
			m = mm.(Model)
		}
	}
}

func TestApplyTheme(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	applyTheme(themeByName("dracula"))
	if string(borderAccent) != "#bd93f9" {
		t.Errorf("dracula accent = %q, want #bd93f9", borderAccent)
	}
	applyTheme(themeByName("default"))
	if string(borderAccent) != "39" {
		t.Errorf("default accent = %q, want 39", borderAccent)
	}
	if themeByName("nope").Name != "default" {
		t.Error("unknown theme should fall back to default")
	}
	if themeIndex("dracula") != 2 {
		t.Errorf("dracula index = %d, want 2", themeIndex("dracula"))
	}
}

// The ? settings screen is a radio list: j/k move through options, a theme row
// previews live, enter selects; tab flips to the keybindings view.
func TestTUI_SettingsScreen(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)

	mm, _ := m.Update(rk("?"))
	m = mm.(Model)
	if !m.showHelp || m.settingsCursor != 0 { // opens on the active theme (default = 0)
		t.Fatalf("? should open settings on the active theme, cursor=%d", m.settingsCursor)
	}

	// j moves onto serika_dark and previews it live — but does NOT commit yet
	mm, _ = m.Update(rk("j"))
	m = mm.(Model)
	if m.settingsCursor != 1 || string(borderAccent) != "#e2b714" {
		t.Errorf("j should preview serika_dark, cursor=%d accent=%q", m.settingsCursor, borderAccent)
	}
	if m.cfg.Theme != "default" {
		t.Errorf("preview must not commit, cfg.Theme=%q", m.cfg.Theme)
	}
	// enter commits the previewed theme
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.cfg.Theme != "serika_dark" {
		t.Errorf("enter should commit theme, got %q", m.cfg.Theme)
	}

	// select the ascii glyph row
	m.settingsCursor = m.glyphAsciiIdx()
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.cfg.StatusGlyphs != "ascii" {
		t.Errorf("selecting ascii should set glyphs=ascii, got %q", m.cfg.StatusGlyphs)
	}

	// editor row: enter -> edit, clear "code", type "vim", enter -> saved
	m.settingsCursor = m.editorIdx()
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if !m.editingOpenCmd {
		t.Fatal("enter on the editor row should start editing")
	}
	for range "code" {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		m = mm.(Model)
	}
	for _, r := range "vim" {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(Model)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.editingOpenCmd || m.cfg.OpenCmd != "vim" {
		t.Errorf("editor edit should save open_cmd=vim, got editing=%v cmd=%q", m.editingOpenCmd, m.cfg.OpenCmd)
	}

	// tab flips to the keys view (which shows the status legend) and back
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = mm.(Model)
	if !m.showKeys || !strings.Contains(stripANSI(m.View()), "PUSH") {
		t.Error("tab should show the keybindings/legend view")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = mm.(Model)
	if m.showKeys {
		t.Error("tab should flip back to settings")
	}

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.(Model).showHelp {
		t.Error("esc should close the settings screen")
	}
}

// Moving onto a theme previews it; closing with esc without selecting reverts to
// the committed theme.
func TestTUI_SettingsPreviewRevert(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = mm.(Model)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}) // preview serika_dark
	m = mm.(Model)
	if string(borderAccent) != "#e2b714" {
		t.Fatalf("preview should apply serika_dark, accent=%q", borderAccent)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // close without selecting
	m = mm.(Model)
	if string(borderAccent) != "39" {
		t.Errorf("esc should revert preview to committed default (39), accent=%q", borderAccent)
	}
}

// editing the editor row and pressing esc must cancel (leave open_cmd unchanged).
func TestTUI_SettingsEditCancel(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	m.showHelp = true
	m.settingsCursor = m.editorIdx()
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = mm.(Model)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.editingOpenCmd || m.cfg.OpenCmd != "code" {
		t.Errorf("esc should cancel the edit, got editing=%v cmd=%q", m.editingOpenCmd, m.cfg.OpenCmd)
	}
	// screen still open (esc only cancelled the edit)
	if !m.showHelp {
		t.Error("esc during edit should not also close the screen")
	}
}
