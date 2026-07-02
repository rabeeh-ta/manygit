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

// The settings + help overlay must fit within the terminal at every size down to
// the documented minimum (80x20) — no line wider than the terminal, no more lines
// than its height — or panelStyle would wrap and break the two-column alignment.
func TestTUI_HelpFitsTerminal(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	for _, d := range []struct{ w, h int }{{80, 20}, {100, 30}, {120, 40}} {
		cfg, repos := twoRepos(t)
		m := loadAll(t, New(cfg, repos, nil), d.w, d.h)
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
		m = mm.(Model)
		lines := strings.Split(m.helpView(), "\n")
		if len(lines) > d.h {
			t.Errorf("%dx%d: overlay is %d lines, exceeds height %d (wrapped)", d.w, d.h, len(lines), d.h)
		}
		for _, ln := range lines {
			if w := lipgloss.Width(ln); w > d.w {
				t.Errorf("%dx%d: line width %d exceeds terminal %d", d.w, d.h, w, d.w)
			}
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

// The ? settings screen: theme cycles + applies live, glyphs toggles, and the
// editor row is an inline text edit — all without touching the real config.
func TestTUI_SettingsScreen(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate config writes
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)

	mm, _ := m.Update(rk("?"))
	m = mm.(Model)
	if !m.showHelp {
		t.Fatal("? should open the settings screen")
	}

	// theme row (cursor 0): l cycles forward and applies live
	mm, _ = m.Update(rk("l"))
	m = mm.(Model)
	if m.cfg.Theme != "serika_dark" || string(borderAccent) != "#e2b714" {
		t.Errorf("l should cycle+apply theme, got %q accent %q", m.cfg.Theme, borderAccent)
	}
	// h wraps backward: serika_dark -> default -> 8008 (last)
	mm, _ = m.Update(rk("h"))
	m = mm.(Model)
	mm, _ = m.Update(rk("h"))
	m = mm.(Model)
	if m.cfg.Theme != "8008" {
		t.Errorf("h should wrap to the last theme, got %q", m.cfg.Theme)
	}

	// glyphs row (cursor 1): toggle unicode -> ascii
	mm, _ = m.Update(rk("j"))
	m = mm.(Model)
	mm, _ = m.Update(rk("l"))
	m = mm.(Model)
	if m.cfg.StatusGlyphs != "ascii" {
		t.Errorf("glyphs toggle should give ascii, got %q", m.cfg.StatusGlyphs)
	}

	// editor row (cursor 2): enter -> edit, clear, type, enter -> saved
	mm, _ = m.Update(rk("j"))
	m = mm.(Model)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if !m.editingOpenCmd {
		t.Fatal("enter on the editor row should start editing")
	}
	for range "code" { // clear the prefilled "code"
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

	// esc closes the screen
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.(Model).showHelp {
		t.Error("esc should close the settings screen")
	}
}

// editing the editor row and pressing esc must cancel (leave open_cmd unchanged).
func TestTUI_SettingsEditCancel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	m.showHelp = true
	m.settingsCursor = 2
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
