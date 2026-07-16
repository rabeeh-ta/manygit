package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"manygit/internal/config"
	"manygit/internal/discover"
	"manygit/internal/harness"
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
		m := loadAll(t, New(cfg, "", repos, nil), d.w, d.h)
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
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

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
	m.settingsCursor = settingRowIndex(skGlyph, "ascii")
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.cfg.StatusGlyphs != "ascii" {
		t.Errorf("selecting ascii should set glyphs=ascii, got %q", m.cfg.StatusGlyphs)
	}

	// editor row: enter -> edit, clear "code", type "vim", enter -> saved
	m.settingsCursor = settingRowIndex(skEditor, "")
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

// The harness setting: the bottom bar shows the active harness; the settings
// screen lists each harness; selecting an installed one sets it while an
// uninstalled one is a no-op.
func TestTUI_HarnessSettingAndBar(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	cfg, repos := twoRepos(t)
	cfg.Harness = "claude"
	m := loadAll(t, New(cfg, "", repos, nil), 120, 30)

	if v := stripANSI(m.View()); !strings.Contains(v, "harness: claude") {
		t.Errorf("bottom bar should show the active harness; view:\n%s", v)
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = mm.(Model)
	sv := stripANSI(m.View())
	for _, want := range []string{"AI harness", "claude", "codex"} {
		if !strings.Contains(sv, want) {
			t.Errorf("settings should list %q", want)
		}
	}
	for _, h := range harness.All {
		m.settingsCursor = settingRowIndex(skHarness, h.Name)
		before := m.cfg.Harness
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = mm.(Model)
		if h.Installed() {
			if m.cfg.Harness != h.Name {
				t.Errorf("selecting installed harness %q should set it, got %q", h.Name, m.cfg.Harness)
			}
		} else if m.cfg.Harness != before {
			t.Errorf("selecting uninstalled harness %q should be a no-op, got %q", h.Name, m.cfg.Harness)
		}
	}
}

// Moving onto a theme previews it; closing with esc without selecting reverts to
// the committed theme.
func TestTUI_SettingsPreviewRevert(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
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
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.showHelp = true
	m.settingsCursor = settingRowIndex(skEditor, "")
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

// pickDepth drives the ? screen to the scan-depth row for val and hits enter,
// returning the model and whatever rescanMsg the command produced.
func pickDepth(t *testing.T, m Model, val string) (Model, tea.Msg) {
	t.Helper()
	m.showHelp = true
	m.settingsCursor = settingRowIndex(skMaxDepth, val)
	if m.settingsCursor < 0 {
		t.Fatalf("no scan-depth row for %q", val)
	}
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		return mm.(Model), nil
	}
	return mm.(Model), cmd()
}

// The scan-depth setting must never be able to empty the Repos pane. main.go
// exits rather than start with zero repos, so the ? screen must not be able to
// reach a state the binary itself refuses to boot into. twoReposIn's repos live
// at <root>/grp/<name>, so depth 1 finds nothing — the change has to be dropped,
// keeping BOTH the old depth and the old list.
func TestTUI_ScanDepthRejectsEmptyResult(t *testing.T) {
	cfg, root, repos := twoReposIn(t)
	m := New(cfg, root, repos, nil)
	m.width, m.height = 100, 30

	m, msg := pickDepth(t, m, "1")
	rs, ok := msg.(rescanMsg)
	if !ok {
		t.Fatalf("expected a rescanMsg, got %T", msg)
	}
	if rs.depth != 1 {
		t.Fatalf("rescan carried depth %d, want 1", rs.depth)
	}
	if len(rs.repos) != 0 {
		t.Fatalf("fixture broken: depth 1 found %d repos, want 0", len(rs.repos))
	}

	mm, _ := m.Update(rs)
	got := mm.(Model)
	if got.cfg.MaxDepth != 3 {
		t.Errorf("depth committed to %d despite an empty walk; want it left at 3", got.cfg.MaxDepth)
	}
	if len(got.repos) != 2 {
		t.Errorf("repo list became %d; the old list must survive a fruitless rescan", len(got.repos))
	}
}

// nestedRepos builds a root with repos at three different depths:
//
//	<root>/top             depth 1
//	<root>/grp/mid         depth 2
//	<root>/grp/sub/deep    depth 3
//
// twoReposIn puts everything at depth 2, so a depth change there leaves the
// count identical and a test can't tell a real swap from a no-op. Here each
// depth yields a different count.
func nestedRepos(t *testing.T) (config.Config, string) {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{"top", "grp/mid", "grp/sub/deep"} {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitCmd(t, dir, "init", "-q", "-b", "master")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitCmd(t, dir, "add", ".")
		gitCmd(t, dir, "commit", "-q", "-m", "init")
	}
	return config.Default(), root
}

// A depth that finds repos commits and swaps the list in. The fixture's count
// changes with depth (1/2/3 repos), so this fails if applyRescan silently keeps
// the old list.
func TestTUI_ScanDepthAppliesWhenReposFound(t *testing.T) {
	cfg, root := nestedRepos(t)
	repos, err := discover.Discover(root, discover.Options{MaxDepth: 3, Prune: cfg.PruneSet()})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 3 {
		t.Fatalf("fixture broken: depth 3 found %d repos, want 3", len(repos))
	}
	m := New(cfg, root, repos, nil)
	m.width, m.height = 100, 30

	m, msg := pickDepth(t, m, "2")
	rs, ok := msg.(rescanMsg)
	if !ok {
		t.Fatalf("expected a rescanMsg, got %T", msg)
	}
	if len(rs.repos) != 2 {
		t.Fatalf("depth 2 found %d repos, want 2 (top + grp/mid)", len(rs.repos))
	}

	mm, _ := m.Update(rs)
	got := mm.(Model)
	if got.cfg.MaxDepth != 2 {
		t.Errorf("MaxDepth = %d, want 2 — a fruitful rescan must commit", got.cfg.MaxDepth)
	}
	if len(got.repos) != 2 {
		t.Errorf("repos = %d, want 2 — the model must take the rescanned list", len(got.repos))
	}
	for _, r := range got.repos {
		if r.repo.Name == "deep" {
			t.Error("depth-3 repo survived a depth-2 rescan")
		}
	}
}

// Widening the depth brings repos back and stats only the new ones.
func TestTUI_ScanDepthWidens(t *testing.T) {
	cfg, root := nestedRepos(t)
	cfg.MaxDepth = 1
	repos, err := discover.Discover(root, discover.Options{MaxDepth: 1, Prune: cfg.PruneSet()})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Fatalf("fixture broken: depth 1 found %d repos, want 1", len(repos))
	}
	m := New(cfg, root, repos, nil)
	m.width, m.height = 100, 30

	m, msg := pickDepth(t, m, "3")
	rs := msg.(rescanMsg)
	mm, _ := m.Update(rs)
	got := mm.(Model)
	if len(got.repos) != 3 {
		t.Errorf("repos = %d, want 3 after widening 1 -> 3", len(got.repos))
	}
	if got.cfg.MaxDepth != 3 {
		t.Errorf("MaxDepth = %d, want 3", got.cfg.MaxDepth)
	}
}

// Re-picking the depth already in effect must not kick off a walk at all.
func TestTUI_ScanDepthNoopOnSameValue(t *testing.T) {
	cfg, root, repos := twoReposIn(t)
	m := New(cfg, root, repos, nil) // cfg.MaxDepth is 3 by default
	m.width, m.height = 100, 30

	if _, msg := pickDepth(t, m, "3"); msg != nil {
		t.Errorf("selecting the active depth produced %T; want no command", msg)
	}
}

// applyRescan must keep the *repoVM of repos that were already on screen — their
// status did not change just because the walk got wider, and rebuilding them
// would blank the list and re-fetch every remote.
func TestTUI_ScanDepthKeepsLoadedRepos(t *testing.T) {
	cfg, root, repos := twoReposIn(t)
	m := loadAll(t, New(cfg, root, repos, nil), 100, 30)
	before := m.repos[0]
	if !before.loaded {
		t.Fatal("fixture broken: repo should be loaded after loadAll")
	}

	m.applyRescan([]discover.Repo{m.repos[0].repo, m.repos[1].repo})
	if m.repos[0] != before {
		t.Error("a surviving repo was rebuilt; its loaded status should carry over")
	}
	if !m.repos[0].loaded {
		t.Error("a surviving repo lost its loaded status across a rescan")
	}
}
