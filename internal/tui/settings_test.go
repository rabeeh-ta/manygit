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
		for _, view := range []string{"keys", "settings"} { // ? lands on keys; tab flips
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
			mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // flip for the 2nd pass
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

// The , settings screen is a radio list: j/k move through options, a theme row
// previews live, enter selects; tab flips to the keybindings view.
func TestTUI_SettingsScreen(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	mm, _ := m.Update(rk(","))
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
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")}) // , opens settings
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
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")}) // , opens settings
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

// tab cycles panels forward and wraps; shift+tab must cycle back and wrap the
// other way. Go's % keeps the sign of the dividend, so a naive focus-1 at the
// first panel would land on -1 rather than the last.
func TestTUI_ShiftTabCyclesBackwards(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	if m.focus != panelRepos {
		t.Fatalf("focus starts at %v, want panelRepos", m.focus)
	}

	// shift+tab from the first panel wraps to the last
	back := func(m Model) Model {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		return mm.(Model)
	}
	want := []panel{panelBottom, panelBranches, panelScripts, panelRepos}
	for i, w := range want {
		m = back(m)
		if m.focus != w {
			t.Fatalf("shift+tab #%d: focus = %v, want %v", i+1, m.focus, w)
		}
	}

	// and it is the exact inverse of tab
	fwd, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	rev, _ := fwd.(Model).Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if rev.(Model).focus != panelRepos {
		t.Errorf("tab then shift+tab = %v, want to land back on panelRepos", rev.(Model).focus)
	}
}

// lipgloss's Width HARD-WRAPS rather than overflowing, so a key wider than the
// column silently breaks across two lines mid-word. Every key label in the
// reference must fit, or the ? -> tab screen renders garbage like "left/rig\nht".
func TestTUI_KeyColumnFitsEveryLabel(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	// ? lands on the keybindings directly now — settings has its own key (,)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	body := stripANSI(mm.(Model).View())

	for _, key := range []string{"left/right", "no-remote", "shift+tab", "b/enter", "5 enter"} {
		if !strings.Contains(body, key) {
			t.Errorf("key %q is wrapped or missing from the reference — Width() hard-wraps", key)
		}
	}
}

// esc must back out of one layer of state per press — and crucially it must
// clear a committed filter, which previously took `/` then esc (two keys to undo
// one, because `/` resets the needle on the way in).
func TestTUI_EscClearsCommittedFilter(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	all := len(m.visibleRepos())
	if all != 2 {
		t.Fatalf("fixture: %d repos, want 2", all)
	}

	// / a l p h a <enter>  -> committed filter, list narrowed
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = mm.(Model)
	for _, r := range "alpha" {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(Model)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.filtering {
		t.Fatal("enter should commit the filter, not keep typing")
	}
	if len(m.visibleRepos()) != 1 {
		t.Fatalf("filter /alpha left %d repos visible, want 1", len(m.visibleRepos()))
	}

	// esc alone must clear it
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.filter != "" {
		t.Errorf("filter = %q after esc, want cleared", m.filter)
	}
	if len(m.visibleRepos()) != all {
		t.Errorf("%d repos visible after esc, want all %d back", len(m.visibleRepos()), all)
	}
	// and it should keep you on the repo you filtered your way to
	if r := m.currentVisible(m.visibleRepos()); r == nil || r.repo.Name != "alpha" {
		got := "(none)"
		if r != nil {
			got = r.repo.Name
		}
		t.Errorf("cursor landed on %s after clearing the filter, want alpha", got)
	}
}

// F is a filter too, so esc must lift it.
func TestTUI_EscClearsAttentionFilter(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	m = mm.(Model)
	if !m.filterAttention {
		t.Fatal("F should turn the attention filter on")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.(Model).filterAttention {
		t.Error("esc should lift the attention filter")
	}
}

// One layer per press, innermost first: diff -> Changes -> zoom -> filter.
// Peeling more than one at a time would yank the user further than they meant.
func TestTUI_EscPeelsOneLayerAtATime(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	esc := func(m Model) Model { mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); return mm.(Model) }

	// stack up: filter, zoom, Changes, diff
	for _, k := range []string{"/", "a", "l"} {
		mm, _ := m.Update(rk(k))
		m = mm.(Model)
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit
	m = mm.(Model)
	mm, _ = m.Update(rk("6")) // Changes
	m = mm.(Model)
	mm, _ = m.Update(rk("z")) // zoom
	m = mm.(Model)
	m.changeShowDiff = true // as if a diff were open

	if m = esc(m); !m.zoomed || m.bottomView != bvChanges || m.changeShowDiff {
		t.Fatalf("esc #1 should close only the diff; zoomed=%v view=%v diff=%v", m.zoomed, m.bottomView, m.changeShowDiff)
	}
	if m = esc(m); !m.zoomed || m.bottomView != bvGraph {
		t.Fatalf("esc #2 should leave only Changes; zoomed=%v view=%v", m.zoomed, m.bottomView)
	}
	if m = esc(m); m.zoomed || m.filter == "" {
		t.Fatalf("esc #3 should unzoom only; zoomed=%v filter=%q", m.zoomed, m.filter)
	}
	if m = esc(m); m.filter != "" {
		t.Fatalf("esc #4 should clear the filter; filter=%q", m.filter)
	}
}

// `]` / `[` cycle the focused pane's TAB BAR, wrapping. They are their own keys
// precisely so `tab` keeps one meaning everywhere — pane cycling — instead of
// changing behaviour depending on which pane you happen to be standing in.
func TestTUI_BracketsCycleTabs(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	press := func(m Model, s string) Model { mm, _ := m.Update(rk(s)); return mm.(Model) }

	// top slot: 2 tabs, wraps
	m = press(m, "3")
	if m.topView != tvBranches {
		t.Fatalf("3 -> topView %v, want tvBranches", m.topView)
	}
	m = press(m, "]")
	if m.topView != tvPRs {
		t.Errorf("] -> topView %v, want tvPRs", m.topView)
	}
	m = press(m, "]")
	if m.topView != tvBranches {
		t.Errorf("] wrap -> topView %v, want tvBranches", m.topView)
	}
	m = press(m, "[")
	if m.topView != tvPRs {
		t.Errorf("[ wrap backwards -> topView %v, want tvPRs", m.topView)
	}

	// bottom slot: 3 tabs, wraps both ways
	m = press(m, "5")
	for i, want := range []bottomView{bvChanges, bvOutput, bvGraph} {
		m = press(m, "]")
		if m.bottomView != want {
			t.Errorf("] #%d -> bottomView %v, want %v", i+1, m.bottomView, want)
		}
	}
	m = press(m, "[")
	if m.bottomView != bvOutput {
		t.Errorf("[ from Graph -> bottomView %v, want bvOutput (wrap)", m.bottomView)
	}
}

// Repos and Scripts have no tab bar, so the brackets must do nothing there —
// not silently jump the user to a pane they didn't ask for.
func TestTUI_BracketsNoopWithoutTabs(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	for _, pane := range []string{"1", "2"} {
		mm, _ := m.Update(rk(pane))
		m = mm.(Model)
		before := m.focus
		for _, k := range []string{"]", "[", "]"} {
			mm, _ = m.Update(rk(k))
			m = mm.(Model)
		}
		if m.focus != before {
			t.Errorf("brackets in pane %s moved focus %v -> %v; want a no-op", pane, before, m.focus)
		}
	}
}

// Every route into a tab must carry the same side effects, or the same
// destination means different state depending on how you got there. Entering the
// PRs tab and filtering, then leaving to Branches by ANY route, must drop the PR
// needle — `3`, `]`, `[`, `right`, and enter-on-Repos all land on Branches.
func TestTUI_EveryRouteToBranchesDropsPRFilter(t *testing.T) {
	cfg, repos := twoRepos(t)
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	routes := map[string]func(Model) Model{
		"3": func(m Model) Model { mm, _ := m.Update(rk("3")); return mm.(Model) },
		"]": func(m Model) Model { mm, _ := m.Update(rk("]")); return mm.(Model) },
		"[": func(m Model) Model { mm, _ := m.Update(rk("[")); return mm.(Model) },
		"right": func(m Model) Model {
			mm, _ := m.Update(rk("1"))
			mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyRight})
			return mm.(Model)
		},
		"enter": func(m Model) Model {
			mm, _ := m.Update(rk("1"))
			mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
			return mm.(Model)
		},
	}
	for name, route := range routes {
		m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
		mm, _ := m.Update(rk("4")) // PRs
		m = mm.(Model)
		mm, _ = m.Update(rk("/")) // filter scoped to the PR sub-view
		m = mm.(Model)
		for _, r := range "zz" {
			mm, _ = m.Update(rk(string(r)))
			m = mm.(Model)
		}
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit
		m = mm.(Model)
		if m.filterPanel != filterPRs || m.filter == "" {
			t.Fatalf("%s: setup failed, filterPanel=%v filter=%q", name, m.filterPanel, m.filter)
		}
		m = route(m)
		if m.topView != tvBranches {
			t.Errorf("%s: topView = %v, want tvBranches", name, m.topView)
		}
		if m.filter != "" {
			t.Errorf("%s: landed on Branches with a live PR needle %q", name, m.filter)
		}
	}
}

// ? is the universal "show me the keys" reflex. It must land on the keybindings,
// never on a settings form — that is the whole point of splitting the two. Each
// key also toggles its OWN page: ? on the keys closes; ? on settings switches.
func TestTUI_HelpAndSettingsAreSeparateKeys(t *testing.T) {
	t.Cleanup(func() { applyTheme(themeByName("default")) })
	cfg, repos := twoRepos(t)
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	press := func(m Model, s string) Model { mm, _ := m.Update(rk(s)); return mm.(Model) }

	// ? -> keys
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m = press(m, "?")
	if !m.showHelp || !m.showKeys {
		t.Fatalf("? -> showHelp=%v showKeys=%v, want both true", m.showHelp, m.showKeys)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "keybindings") {
		t.Error("? should render the keybindings page")
	}
	// ? again closes it
	if m = press(m, "?"); m.showHelp {
		t.Error("? on the keys page should close the overlay")
	}

	// , -> settings, on the active theme
	m = press(m, ",")
	if !m.showHelp || m.showKeys {
		t.Fatalf(", -> showHelp=%v showKeys=%v, want settings", m.showHelp, m.showKeys)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "settings") {
		t.Error(", should render the settings page")
	}
	if m.settingsCursor != themeIndex(m.cfg.Theme) {
		t.Errorf("settingsCursor = %d, want the active theme row %d", m.settingsCursor, themeIndex(m.cfg.Theme))
	}
	// , again closes it
	if m = press(m, ","); m.showHelp {
		t.Error(", on the settings page should close the overlay")
	}

	// tab still flips between the two
	m = press(m, "?")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = mm.(Model)
	if m.showKeys {
		t.Error("tab from the keys page should flip to settings")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !mm.(Model).showKeys {
		t.Error("tab from settings should flip back to the keys")
	}
}

// The keybindings page is the ONLY place that names `,`, so it is the entire
// discovery chain for settings. If it stops listing it, settings is unreachable
// for anyone who doesn't already know the key.
func TestTUI_KeysPageDocumentsSettingsKey(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	v := stripANSI(mm.(Model).View())
	for _, want := range []string{"This screen", ",", "settings"} {
		if !strings.Contains(v, want) {
			t.Errorf("the keys page must mention %q — it is how you find settings", want)
		}
	}
}
