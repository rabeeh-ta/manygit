package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rabeeh-ta/manygit/internal/config"
	"github.com/rabeeh-ta/manygit/internal/discover"
	"github.com/rabeeh-ta/manygit/internal/git"
)

// locate finds text in the rendered view and returns its screen cell. The
// column is measured in display cells, not bytes — the borders are multi-byte.
// Every mouse test clicks where View() actually DREW something, so the
// hit-test is checked against the renderer rather than against its own maths.
func locate(t *testing.T, m Model, text string) (x, y int) {
	t.Helper()
	for y, line := range strings.Split(stripANSI(m.View()), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return lipgloss.Width(line[:i]), y
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, stripANSI(m.View()))
	return 0, 0
}

func click(m Model, x, y int) (Model, tea.Cmd) {
	mm, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return mm.(Model), cmd
}

func wheel(m Model, x, y int, down bool) Model {
	b := tea.MouseButtonWheelUp
	if down {
		b = tea.MouseButtonWheelDown
	}
	mm, _ := m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
	return mm.(Model)
}

// mouseModel is 30 repos across three groups, so the Repos pane both scrolls and
// interleaves group headers — the two things that make a screen row differ from
// a repo index.
func mouseModel(t *testing.T) Model {
	t.Helper()
	var repos []discover.Repo
	for i := 0; i < 30; i++ {
		repos = append(repos, discover.Repo{
			Path:  fmt.Sprintf("/x/r%02d", i),
			Name:  fmt.Sprintf("repo-%02d", i),
			Group: fmt.Sprintf("group-%d", i/10),
		})
	}
	scripts := []discover.Script{{Path: "/x/a.sh", Name: "a.sh"}, {Path: "/x/b.sh", Name: "b.sh"}, {Path: "/x/c.sh", Name: "c.sh"}}
	m := New(config.Default(), "", repos, scripts)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return mm.(Model)
}

func TestMouse_ClickRepoRowSelectsIt(t *testing.T) {
	m := mouseModel(t)
	m.focus = panelScripts
	x, y := locate(t, m, "repo-12") // below two group headers
	m, cmd := click(m, x, y)
	if m.focus != panelRepos {
		t.Errorf("click should focus Repos, got %v", m.focus)
	}
	if got := m.visibleRepos()[m.cursor].repo.Name; got != "repo-12" {
		t.Errorf("clicked repo-12, cursor is on %s", got)
	}
	if cmd == nil {
		t.Error("moving the repo cursor must load its branches + graph, like j/k")
	}
}

// With the list scrolled, the first row on screen is not repo 0 — the click has
// to go through the same window() the renderer used.
func TestMouse_ClickRepoRowWhenScrolled(t *testing.T) {
	m := mouseModel(t)
	m.cursor = 29
	x, y := locate(t, m, "repo-25")
	m, _ = click(m, x, y)
	if got := m.visibleRepos()[m.cursor].repo.Name; got != "repo-25" {
		t.Errorf("clicked repo-25 in a scrolled list, cursor is on %s", got)
	}
}

func TestMouse_ClickGroupHeaderOnlyFocuses(t *testing.T) {
	m := mouseModel(t)
	m.cursor = 3
	m.focus = panelBottom
	x, y := locate(t, m, "group-1")
	m, _ = click(m, x, y)
	if m.focus != panelRepos || m.cursor != 3 {
		t.Errorf("a header is not a row: want focus Repos + cursor 3, got %v + %d", m.focus, m.cursor)
	}
}

func TestMouse_ClickScriptSelectsButNeverRuns(t *testing.T) {
	m := mouseModel(t)
	x, y := locate(t, m, "c.sh")
	m, cmd := click(m, x, y)
	if m.focus != panelScripts || m.scriptCursor != 2 {
		t.Errorf("want Scripts focused on c.sh, got %v cursor %d", m.focus, m.scriptCursor)
	}
	if cmd != nil || m.outputRunning {
		t.Error("a click must not run a script — enter does that")
	}
}

func TestMouse_ClickTabs(t *testing.T) {
	m := mouseModel(t)
	x, y := locate(t, m, "4 PRs")
	m, _ = click(m, x, y)
	if m.focus != panelBranches || m.topView != tvPRs {
		t.Errorf("clicking the PRs chip should show PRs, got focus %v view %v", m.focus, m.topView)
	}
	x, y = locate(t, m, "7 Output")
	m, _ = click(m, x+3, y) // anywhere on the chip, not just its first cell
	if m.focus != panelBottom || m.bottomView != bvOutput {
		t.Errorf("clicking the Output chip should show Output, got focus %v view %v", m.focus, m.bottomView)
	}
	x, y = locate(t, m, "3 Branches")
	m, _ = click(m, x, y)
	if m.topView != tvBranches {
		t.Errorf("clicking the Branches chip should show Branches, got %v", m.topView)
	}
}

func TestMouse_ClickBranchSelectsButNeverChecksOut(t *testing.T) {
	m := mouseModel(t)
	m.branches = []git.Branch{{Name: "main", IsCurrent: true}, {Name: "feature-x"}, {Name: "origin/main", IsRemote: true}}
	x, y := locate(t, m, "feature-x")
	m, cmd := click(m, x, y)
	if m.focus != panelBranches || m.branchCursor != 1 {
		t.Errorf("want Branches focused on feature-x, got %v cursor %d", m.focus, m.branchCursor)
	}
	if cmd != nil {
		t.Error("a click must not check out — enter does that")
	}
}

func TestMouse_ClickGraphCommitAndSkipConnectors(t *testing.T) {
	m := mouseModel(t)
	m.graphLines = []string{"* aaa first", "|\\", "* bbb second"}
	m.graphCommits = []git.GraphEntry{{Hash: "aaa", Line: 0}, {Hash: "bbb", Line: 2}}
	x, y := locate(t, m, "bbb second")
	m, _ = click(m, x, y)
	if m.focus != panelBottom || m.graphSel != 2 {
		t.Errorf("want graph commit bbb (sel 2), got focus %v sel %d", m.focus, m.graphSel)
	}
	x, y = locate(t, m, "|\\")
	m, _ = click(m, x, y)
	if m.graphSel != 2 {
		t.Errorf("a connector line is not a commit; selection moved to %d", m.graphSel)
	}
	x, y = locate(t, m, "WIP (uncommitted")
	m, _ = click(m, x, y)
	if m.graphSel != 0 {
		t.Errorf("clicking WIP should select it, got %d", m.graphSel)
	}
}

func TestMouse_WheelMovesPaneUnderPointer(t *testing.T) {
	m := mouseModel(t)
	m.focus = panelBottom
	x, y := locate(t, m, "repo-02")
	m = wheel(m, x, y, true)
	m = wheel(m, x, y, true)
	if m.focus != panelRepos || m.cursor != 2 {
		t.Errorf("two wheel-downs over Repos: want focus Repos cursor 2, got %v %d", m.focus, m.cursor)
	}
	m = wheel(m, x, y, false)
	if m.cursor != 1 {
		t.Errorf("wheel-up should move back, got %d", m.cursor)
	}
}

func TestMouse_ZoomedPaneTakesClicks(t *testing.T) {
	m := mouseModel(t)
	m.zoomed = true
	x, y := locate(t, m, "repo-07")
	m, _ = click(m, x, y)
	if got := m.visibleRepos()[m.cursor].repo.Name; got != "repo-07" {
		t.Errorf("clicked repo-07 in the zoomed pane, cursor is on %s", got)
	}
}

// A click is not an answer. The y/N confirms treat any KEY as "no", but letting
// a pointer event through would cancel a discard you were still reading — or
// move the cursor so the confirm's own text lies about which repo it hits.
func TestMouse_BlockedDuringConfirmsAndPrompts(t *testing.T) {
	base := mouseModel(t)
	x, y := locate(t, base, "repo-05")
	for name, arm := range map[string]func(*Model){
		"discard": func(m *Model) { m.confirmDiscard = true },
		"filter":  func(m *Model) { m.filtering = true },
		"shell":   func(m *Model) { m.shellPrompting = true },
		"ai":      func(m *Model) { m.aiPrompting = true },
	} {
		m := base
		arm(&m)
		m, _ = click(m, x, y)
		if m.cursor != 0 {
			t.Errorf("%s: a click moved the cursor to %d", name, m.cursor)
		}
		if name == "discard" && !m.confirmDiscard {
			t.Errorf("discard: a click must not cancel the confirm")
		}
	}
}

func TestMouse_OffIgnoresEverything(t *testing.T) {
	m := mouseModel(t)
	m.cfg.Mouse = "off"
	x, y := locate(t, m, "repo-05")
	m, _ = click(m, x, y)
	if m.cursor != 0 {
		t.Errorf("mouse: off, but a click moved the cursor to %d", m.cursor)
	}
}

// Picking the setting has to reach the terminal NOW — otherwise "off" wouldn't
// give drag-to-select back until a restart.
func TestMouse_SettingTogglesReportingLive(t *testing.T) {
	m := mouseModel(t)
	m.showHelp, m.showKeys = true, false
	m.settingsCursor = settingRowIndex(skMouse, "off")
	cmd := m.settingsSelect()
	if m.cfg.MouseEnabled() {
		t.Fatal("selecting off should disable the mouse")
	}
	if cmd == nil {
		t.Fatal("selecting off must tell the terminal to stop reporting")
	}
	if fmt.Sprintf("%T", cmd()) != fmt.Sprintf("%T", tea.DisableMouse()) {
		t.Errorf("off should send DisableMouse, got %T", cmd())
	}
	m.settingsCursor = settingRowIndex(skMouse, "on")
	if cmd = m.settingsSelect(); cmd == nil || fmt.Sprintf("%T", cmd()) != fmt.Sprintf("%T", tea.EnableMouseCellMotion()) {
		t.Error("on should send EnableMouseCellMotion")
	}
}

// Wheeling over the settings face would flash the app through every theme (j/k
// there previews live), so only the keys face scrolls.
func TestMouse_WheelOnSettingsFaceIsInert(t *testing.T) {
	m := mouseModel(t)
	m.showHelp, m.showKeys = true, false
	m.settingsCursor = 0
	m = wheel(m, 10, 10, true)
	if m.settingsCursor != 0 {
		t.Errorf("wheel moved the settings cursor to %d", m.settingsCursor)
	}
	m.showKeys = true
	m.height = minTermH // short enough that the keys face overflows
	m = wheel(m, 10, 10, true)
	if m.keysOffset != 1 {
		t.Errorf("wheel on the keys face should scroll it, offset %d", m.keysOffset)
	}
}
