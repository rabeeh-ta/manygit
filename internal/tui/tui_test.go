package tui

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"

	"manygit/internal/config"
	"manygit/internal/discover"
	"manygit/internal/gh"
	"manygit/internal/git"
)

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// isASCII reports whether s is pure ASCII (every rune < 128). Decorative UI
// glyphs must be ASCII so ambiguous-East-Asian-width terminals render them
// exactly one cell wide and columns never drift.
func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// twoRepos builds a root with two committed repos ("alpha","bravo") in group "grp".
func twoRepos(t *testing.T) (config.Config, []discover.Repo) {
	t.Helper()
	cfg, _, repos := twoReposIn(t)
	return cfg, repos
}

// twoReposIn is twoRepos plus the root it scanned, for tests that re-walk it.
// The repos sit at <root>/grp/<name> — depth 2 — so a depth-1 scan finds nothing.
func twoReposIn(t *testing.T) (config.Config, string, []discover.Repo) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"alpha", "bravo"} {
		dir := filepath.Join(root, "grp", name)
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
	cfg := config.Default()
	repos, err := discover.Discover(root, discover.Options{MaxDepth: 3, Prune: cfg.PruneSet()})
	if err != nil {
		t.Fatal(err)
	}
	return cfg, root, repos
}

// loadAll applies WindowSizeMsg + a statusMsg per repo, returning the settled model.
func loadAll(t *testing.T, m Model, w, h int) Model {
	t.Helper()
	mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = mm.(Model)
	for _, r := range m.repos {
		u, _ := m.Update(statusMsg{path: r.repo.Path, st: git.Status(r.repo.Path)})
		m = u.(Model)
	}
	return m
}

func TestTUI_RendersReposAndQuits(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, "", repos, nil), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("alpha")) && bytes.Contains(b, []byte("bravo"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTUI_CursorMovesDown(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, "", repos, nil), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("alpha"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(Model)
	if fm.cursor != 1 {
		t.Errorf("cursor = %d, want 1", fm.cursor)
	}
}

var _ = lipgloss.Width // used by the spacing test in Task 10
var _ = strings.Split

// enter drills into the highlighted repo's branches; space is unbound and must
// leave the focus untouched in both panels.
func TestTUI_EnterFocusesBranches(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30) // focus starts on Repos
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if mm.(Model).focus != panelRepos {
		t.Error("space in the Repos panel should do nothing")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.focus != panelBranches {
		t.Errorf("enter in Repos panel should focus Branches, got %v", m.focus)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if mm.(Model).focus != panelBranches {
		t.Error("space in the Branches panel should do nothing")
	}
}

// →/← hop between Repos and Branches, and nowhere else: from any other panel
// they stay unbound, and while a `/` filter is being typed the filter handler
// swallows them (so an arrow can't yank focus mid-search).
func TestTUI_ArrowsHopReposBranches(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30) // focus starts on Repos

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = mm.(Model)
	if m.focus != panelBranches {
		t.Fatalf("right in Repos should focus Branches, got %v", m.focus)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = mm.(Model)
	if m.focus != panelRepos {
		t.Fatalf("left in Branches should focus Repos, got %v", m.focus)
	}
	// left from Repos / right from Branches are dead ends, not a wrap-around.
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if mm.(Model).focus != panelRepos {
		t.Error("left in Repos should do nothing")
	}
	// Unbound in the other panels.
	m.focus = panelScripts
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if mm.(Model).focus != panelScripts {
		t.Error("right in Scripts should do nothing")
	}
	m.focus = panelBottom
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if mm.(Model).focus != panelBottom {
		t.Error("left in the bottom slot should do nothing")
	}
	// While typing a filter, arrows must not move focus.
	m.focus = panelRepos
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = mm.(Model)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = mm.(Model)
	if m.focus != panelRepos || !m.filtering {
		t.Errorf("right while filtering should be swallowed, got focus=%v filtering=%v", m.focus, m.filtering)
	}
}

func TestTUI_FilterNarrowsList(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, "", repos, nil), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("bravo"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "alp" {
		tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Commit the filter with enter (which, unlike esc, keeps m.filter set) and
	// assert against the settled model state rather than screen-scraping the
	// teatest output stream: with the two-column view now occupying the full
	// terminal height, Bubble Tea's tick-based renderer can coalesce the
	// filter keystrokes into a single repaint that never re-touches alpha's
	// row bytes (its content is unchanged by the filter), making a
	// byte-presence assertion for "alpha" racy. Checking visibleRepos()
	// directly is deterministic and equally faithful to the behavior under
	// test (only alpha remains visible after filtering to "alp").
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(Model)
	vis := fm.visibleRepos()
	if len(vis) != 1 || vis[0].repo.Name != "alpha" {
		names := make([]string, len(vis))
		for i, r := range vis {
			names[i] = r.repo.Name
		}
		t.Errorf("visibleRepos after filtering to %q = %v, want [alpha]", fm.filter, names)
	}
}

// The Scripts panel lists discovered scripts; j/k move its cursor and enter
// (when it's focused) builds a run command for the highlighted script.
func TestTUI_ScriptsPanel(t *testing.T) {
	cfg, repos := twoRepos(t)
	scripts := []discover.Script{
		{Path: "/x/a.sh", Name: "a.sh"},
		{Path: "/x/scripts/b.sh", Name: "scripts/b.sh"},
	}
	m := loadAll(t, New(cfg, "", repos, scripts), 100, 30)
	m.focus = panelScripts

	if v := stripANSI(m.View()); !strings.Contains(v, "a.sh") || !strings.Contains(v, "scripts/b.sh") {
		t.Errorf("Scripts panel should render script names")
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(Model)
	if m.scriptCursor != 1 {
		t.Errorf("j in Scripts panel should move scriptCursor, got %d", m.scriptCursor)
	}
	// enter in the Scripts panel yields a (non-nil) run command; it does not run here.
	if m.runScriptCmd() == nil {
		t.Errorf("expected a run command for the highlighted script")
	}
	// with no scripts, run is a no-op.
	empty := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	empty.focus = panelScripts
	if empty.runScriptCmd() != nil {
		t.Errorf("runScriptCmd with no scripts should be nil")
	}
}

// `/` in the Scripts panel filters the scripts list (not the repos list); the
// cursor and run target the filtered view.
func TestTUI_SlashFiltersScripts(t *testing.T) {
	cfg, repos := twoRepos(t)
	scripts := []discover.Script{
		{Path: "/x/sync-edx.sh", Name: "sync-edx.sh"},
		{Path: "/x/sync-mfe.sh", Name: "sync-mfe.sh"},
		{Path: "/x/deploy.sh", Name: "deploy.sh"},
	}
	m := loadAll(t, New(cfg, "", repos, scripts), 100, 30)
	m.focus = panelScripts

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = mm.(Model)
	if m.filterPanel != panelScripts {
		t.Fatalf("`/` in Scripts should target the scripts list, got %d", m.filterPanel)
	}
	for _, r := range "sync" {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(Model)
	}
	if len(m.visibleScripts()) != 2 {
		t.Fatalf("filtering scripts to \"sync\" -> %d, want 2", len(m.visibleScripts()))
	}
	if len(m.visibleRepos()) != len(repos) {
		t.Error("a scripts-scoped filter must not narrow the repos list")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit filter
	m = mm.(Model)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // run the highlighted script
	m = mm.(Model)
	if m.outputTitle != "sync-edx.sh" {
		t.Errorf("enter should run the highlighted filtered script, got %q", m.outputTitle)
	}
}

// `/` in the Branches panel filters the branch list (not the repos list) — the
// only practical way through a repo's hundreds of remote refs — and the cursor
// and checkout both index the filtered view.
func TestTUI_SlashFiltersBranches(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.focus = panelBranches
	m.branches = []git.Branch{
		{Name: "master", IsCurrent: true},
		{Name: "origin/feat/aisuite-onboarding", IsRemote: true},
		{Name: "origin/fix/search_filter", IsRemote: true},
	}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = mm.(Model)
	if m.filterPanel != panelBranches {
		t.Fatalf("`/` in Branches should target the branch list, got %d", m.filterPanel)
	}
	for _, r := range "feat" {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(Model)
	}
	vb := m.visibleBranches()
	if len(vb) != 1 || vb[0].Name != "origin/feat/aisuite-onboarding" {
		t.Fatalf("filtering branches to \"feat\" -> %+v, want [origin/feat/aisuite-onboarding]", vb)
	}
	if len(m.visibleRepos()) != len(repos) {
		t.Error("a branches-scoped filter must not narrow the repos list")
	}
	if len(m.visibleScripts()) != len(m.scripts) {
		t.Error("a branches-scoped filter must not narrow the scripts list")
	}
	if v := stripANSI(m.View()); strings.Contains(v, "search_filter") {
		t.Error("the Branches panel should render only the matching branches")
	}
	// The cursor indexes the FILTERED list, so enter checks out the match (not
	// whatever sat at that index in the unfiltered list).
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit the filter
	m = mm.(Model)
	if m.branchCursor != 0 || m.visibleBranches()[m.branchCursor].LocalName() != "feat/aisuite-onboarding" {
		t.Errorf("checkout target = %+v, want feat/aisuite-onboarding", m.visibleBranches()[m.branchCursor])
	}
}

// A committed branch filter belongs to one repo, so moving the repo cursor
// clears it — otherwise the stale needle silently filters the next repo's
// branches. A committed *repo* filter, by contrast, must survive repo navigation.
func TestTUI_BranchFilterClearsOnRepoChange(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.focus = panelBranches
	m.branches = []git.Branch{
		{Name: "master", IsCurrent: true},
		{Name: "origin/feat/x", IsRemote: true},
	}
	// Commit a branch filter, then hop back to Repos and move the cursor.
	m.filter, m.filterPanel = "feat", panelBranches
	if len(m.visibleBranches()) != 1 {
		t.Fatalf("precondition: branch filter should show 1 branch, got %d", len(m.visibleBranches()))
	}
	m.focus = panelRepos
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown}) // next repo
	m = mm.(Model)
	if m.filter != "" || m.filterPanel == panelBranches {
		t.Errorf("moving the repo cursor should clear the branch filter, got filter=%q panel=%d", m.filter, m.filterPanel)
	}
	if len(m.visibleBranches()) != len(m.branches) {
		t.Error("after clearing, all branches should be visible again")
	}

	// A committed REPO filter must NOT be wiped by repo navigation.
	m2 := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m2.focus = panelRepos
	m2.filter, m2.filterPanel = "a", panelRepos // matches both alpha and bravo... narrow later
	mm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyDown})
	m2 = mm.(Model)
	if m2.filter != "a" || m2.filterPanel != panelRepos {
		t.Errorf("a repo filter must survive repo navigation, got filter=%q panel=%d", m2.filter, m2.filterPanel)
	}
}

// Pressing p before a repo's status has loaded skips with a reason instead of
// running git push blind (a local-only repo would fail "No configured push
// destination"), mirroring the s handler.
func TestTUI_PushSkipsUnloadedRepo(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := New(cfg, "", repos, nil) // no loadAll: repos start unloaded
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = mm.(Model)
	if m.currentVisible(m.visibleRepos()).loaded {
		t.Fatal("precondition: repo should be unloaded")
	}
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd == nil {
		t.Fatal("p should produce a command")
	}
	mm, _ = mm.(Model).Update(cmd())
	if got := stripANSI(mm.(Model).statusLine); !strings.Contains(got, "skipped: status not loaded yet") {
		t.Errorf("push on an unloaded repo: status = %q, want skipped: status not loaded yet", got)
	}
}

// Long ref names in the graph decorations are shortened when the graph loads,
// leaving the commit subject intact.
func TestTUI_GraphRefsShortened(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	path := m.currentVisible(m.visibleRepos()).repo.Path
	long := "* \x1b[1;32m" + strings.Repeat("z", 40) + "\x1b[m short: subject"
	mm, _ := m.Update(graphMsg{path: path, lines: []string{long}})
	m = mm.(Model)
	plain := stripANSI(m.graphLines[0])
	if strings.Contains(plain, strings.Repeat("z", 40)) {
		t.Errorf("long ref should be shortened in the stored graph line: %q", plain)
	}
	if !strings.Contains(plain, "subject") {
		t.Errorf("commit subject should survive shortening: %q", plain)
	}
}

// A repo row shows the current branch in dim parens after the name, truncated to
// fit, and rows stay equal width so the columns don't drift.
func TestTUI_RepoRowShowsBranch(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 30)
	d := computeDims(120, 30, false)
	m.repos[0].status = git.RepoStatus{Branch: "main"}
	m.repos[1].status = git.RepoStatus{Branch: "feature/really-long-branch-that-overflows-the-column"}

	r0 := m.renderRow(0, m.repos[0], d.nameW)
	r1 := m.renderRow(1, m.repos[1], d.nameW)
	if !strings.Contains(stripANSI(r0), "(main)") {
		t.Errorf("row should show the current branch, got %q", stripANSI(r0))
	}
	if lipgloss.Width(r0) != lipgloss.Width(r1) {
		t.Errorf("rows must be equal width: %d vs %d", lipgloss.Width(r0), lipgloss.Width(r1))
	}
	if strings.Contains(stripANSI(r1), "overflows") {
		t.Errorf("a long branch should be truncated, got %q", stripANSI(r1))
	}
	// no branch (e.g. an errored repo) -> no parens, same width
	m.repos[0].status = git.RepoStatus{}
	rEmpty := m.renderRow(0, m.repos[0], d.nameW)
	if strings.Contains(stripANSI(rEmpty), "(") {
		t.Errorf("no branch should show no parens, got %q", stripANSI(rEmpty))
	}
	if lipgloss.Width(rEmpty) != lipgloss.Width(r0) {
		t.Error("empty-branch row width should match a branch row")
	}

	// Every row must be a single physical line, even with wide-character (CJK)
	// branch names — a wrapped row would shove the dirty/status columns off-axis.
	// (lipgloss.Width alone can't catch this: it returns the max width across the
	// wrapped lines, which Width(nameW) has padded back to the same value.)
	for _, br := range []string{"main", "功能分支名称非常长的中文分支", "🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀"} {
		m.repos[0].status = git.RepoStatus{Branch: br}
		row := m.renderRow(0, m.repos[0], d.nameW)
		if strings.Contains(row, "\n") {
			t.Errorf("row for branch %q wrapped to multiple lines: %q", br, stripANSI(row))
		}
		if lipgloss.Width(row) != lipgloss.Width(rEmpty) {
			t.Errorf("row for branch %q width %d != %d", br, lipgloss.Width(row), lipgloss.Width(rEmpty))
		}
	}
}

// titledBox must keep the top border aligned even when the label contains
// non-ASCII characters (e.g. an accented repo name in the graph title).
func TestTUI_TitledBoxNonASCIILabel(t *testing.T) {
	box := titledBox("Graph: café-répo (esc close)", 40, 5, true, "content")
	want := -1
	for _, ln := range strings.Split(box, "\n") {
		if lw := lipgloss.Width(ln); want < 0 {
			want = lw
		} else if lw != want {
			t.Errorf("titledBox line width %d != %d: %q", lw, want, ln)
		}
	}
}

// Branch names are shown in full in the panel (the panel has ample width).
func TestTUI_BranchNamesShownInFull(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.focus = panelBranches
	m.branches = []git.Branch{
		{Name: "PROJ-1234-implement-the-new-onboarding-flow"},
		{Name: "master", IsCurrent: true},
	}
	out := stripANSI(m.renderBranches(80, 10))
	if !strings.Contains(out, "PROJ-1234-implement-the-new-onboarding-flow") {
		t.Errorf("long branch name should be shown in full, got:\n%s", out)
	}
	if !strings.Contains(out, "master") {
		t.Error("current branch should be shown")
	}
}

// g opens a full-screen graph overlay over the already-loaded graph; j/k scroll,
// esc closes.
func TestTUI_GraphOverlay(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	// simulate loadContextCmd having loaded the graph
	path := m.currentVisible(m.visibleRepos()).repo.Path
	mm, _ := m.Update(graphMsg{path: path, lines: []string{"* a", "* b", "* c"},
		commits: []git.GraphEntry{{Line: 0, Hash: "aaaaaaa"}, {Line: 1, Hash: "bbbbbbb"}, {Line: 2, Hash: "ccccccc"}}})
	m = mm.(Model)
	if len(m.graphLines) != 3 || len(m.graphCommits) != 3 {
		t.Fatalf("graphMsg should populate graph, got %d lines %d commits", len(m.graphLines), len(m.graphCommits))
	}
	// g opens the overlay (reusing the loaded graph)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = mm.(Model)
	if !m.showGraph {
		t.Fatal("g should open the graph overlay")
	}
	if !strings.Contains(stripANSI(m.View()), "Graph:") {
		t.Error("graph overlay should show a Graph: title")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(Model)
	if m.graphOffset != 1 {
		t.Errorf("j should scroll the graph, offset=%d", m.graphOffset)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.(Model).showGraph {
		t.Error("esc should close the graph overlay")
	}
}

// The bottom multi-view slot: 4 Graph (with commit/WIP selection) -> 5 Changes
// (files of the selection) -> enter opens the diff -> esc back; 6 Output.
func TestTUI_BottomViewsAndSelection(t *testing.T) {
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	path := m.currentVisible(m.visibleRepos()).repo.Path
	mm, _ := m.Update(graphMsg{path: path, lines: []string{"* aaaaaaa fix", "* bbbbbbb add"},
		commits: []git.GraphEntry{{Line: 0, Hash: "aaaaaaa"}, {Line: 1, Hash: "bbbbbbb"}}})
	m = mm.(Model)

	mm, _ = m.Update(rk("5"))
	m = mm.(Model)
	if m.focus != panelBottom || m.bottomView != bvGraph {
		t.Fatal("5 should focus the graph view")
	}
	if m.selectedRef() != "" {
		t.Errorf("WIP ref should be empty, got %q", m.selectedRef())
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // WIP -> first commit
	m = mm.(Model)
	if m.graphSel != 1 || m.selectedRef() != "aaaaaaa" {
		t.Errorf("j should select aaaaaaa, sel=%d ref=%q", m.graphSel, m.selectedRef())
	}
	mm, cmd := m.Update(rk("6"))
	m = mm.(Model)
	if m.bottomView != bvChanges || cmd == nil {
		t.Fatal("6 should switch to Changes and load the selection's files")
	}
	mm, _ = m.Update(changesMsg{path: path, ref: "aaaaaaa",
		files: []git.FileChange{{Status: "M", Path: "foo.go"}, {Status: "A", Path: "bar.go"}}})
	m = mm.(Model)
	if len(m.changeFiles) != 2 {
		t.Fatalf("changes should have 2 files, got %d", len(m.changeFiles))
	}
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("enter should load the selected file's diff")
	}
	mm, _ = m.Update(diffMsg{path: path, ref: "aaaaaaa", lines: []string{"@@ -1 +1 @@", "-old", "+new"}})
	m = mm.(Model)
	if !m.changeShowDiff || len(m.changeDiff) != 3 {
		t.Fatal("diffMsg should show the diff in-place")
	}
	// a stale diff (wrong ref) must be dropped
	m.changeShowDiff = false
	mm, _ = m.Update(diffMsg{path: path, ref: "zzzzzzz", lines: []string{"stale"}})
	if mm.(Model).changeShowDiff {
		t.Error("a stale diff (wrong ref) should be dropped")
	}
	m.changeShowDiff = true
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.changeShowDiff {
		t.Error("esc should close the diff")
	}
	mm, _ = m.Update(rk("7"))
	if mm.(Model).bottomView != bvOutput {
		t.Error("7 should switch to Output")
	}
}

// From the Graph, enter drills into the selected commit's changed files; enter on
// a file opens its diff; esc walks back diff → files → graph.
func TestTUI_GraphDrillDown(t *testing.T) {
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	path := m.currentVisible(m.visibleRepos()).repo.Path
	mm, _ := m.Update(graphMsg{path: path, lines: []string{"* aaaaaaa first"},
		commits: []git.GraphEntry{{Line: 0, Hash: "aaaaaaa"}}})
	m = mm.(Model)

	mm, _ = m.Update(rk("5")) // Graph
	m = mm.(Model)
	mm, _ = m.Update(rk("j")) // select the commit (graphSel 1)
	m = mm.(Model)
	if m.graphSel != 1 {
		t.Fatalf("j should select the commit, graphSel=%d", m.graphSel)
	}
	// enter drills into that commit's Changes
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.bottomView != bvChanges || cmd == nil {
		t.Fatal("enter on the graph should drill into Changes and load its files")
	}
	mm, _ = m.Update(changesMsg{path: path, ref: "aaaaaaa", files: []git.FileChange{{Status: "M", Path: "x.go"}}})
	m = mm.(Model)
	if len(m.changeFiles) != 1 {
		t.Fatal("the commit's files should load")
	}
	// enter on the file opens its diff
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("enter on a file should load its diff")
	}
	mm, _ = m.Update(diffMsg{path: path, ref: "aaaaaaa", lines: []string{"@@ -1 +1 @@", "-a", "+b"}})
	m = mm.(Model)
	if !m.changeShowDiff {
		t.Fatal("diff should be shown in-place")
	}
	// esc: diff → file list
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.changeShowDiff || m.bottomView != bvChanges {
		t.Error("esc from the diff should return to the file list")
	}
	// esc: file list → graph
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.bottomView != bvGraph {
		t.Error("esc from the file list should return to the graph")
	}
}

// A long repos list windows to keep the cursor visible (scrolls) instead of
// running off the bottom of the panel.
func TestTUI_ReposScroll(t *testing.T) {
	var repos []discover.Repo
	for i := 0; i < 20; i++ {
		repos = append(repos, discover.Repo{Path: fmt.Sprintf("/x/r%02d", i), Name: fmt.Sprintf("repo-%02d", i), Group: "g"})
	}
	m := New(config.Default(), "", repos, nil)
	m.width, m.height = 100, 20
	d := computeDims(100, 20, false)

	m.cursor = 18 // deep in the list
	body := stripANSI(m.renderRepoBody(d, 6))
	if !strings.Contains(body, "repo-18") {
		t.Errorf("a deep cursor (repo-18) must stay visible:\n%s", body)
	}
	if strings.Contains(body, "repo-00") {
		t.Error("a deep cursor should scroll the top of the list off")
	}
	m.cursor = 0
	if !strings.Contains(stripANSI(m.renderRepoBody(d, 6)), "repo-00") {
		t.Error("cursor at the top should show the first repo")
	}
}

// With the Changes view (5) open, browsing repos in the Repos panel must refresh
// it to follow the highlighted repo (it used to stay stuck on the first repo).
func TestTUI_ChangesFollowsRepoCursor(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	// open Changes (6), then focus Repos (1)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("6")})
	m = mm.(Model)
	if m.bottomView != bvChanges {
		t.Fatal("6 should show the Changes view")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = mm.(Model)

	// move the repo cursor to the second repo
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = mm.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor should have moved to 1, got %d", m.cursor)
	}

	// the resulting command must include a changes reload for the NEW repo
	want := m.repos[1].repo.Path
	found := false
	for _, msg := range drainCmd(cmd) {
		if cm, ok := msg.(changesMsg); ok && cm.path == want {
			found = true
		}
	}
	if !found {
		t.Error("moving the repo cursor with Changes open should refresh it for the new repo")
	}
}

// drainCmd runs a (possibly batched) command and returns every message it emits.
func drainCmd(c tea.Cmd) []tea.Msg {
	if c == nil {
		return nil
	}
	switch msg := c().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, sub := range msg {
			out = append(out, drainCmd(sub)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func TestFitNameSuffixes(t *testing.T) {
	cases := []struct {
		name, branch, tag   string
		w                   int
		wantN, wantB, wantT string
	}{
		// everything fits
		{"abc", "main", "v1.2", 30, "abc", "main", "v1.2"},
		// tag doesn't fit whole → dropped (never truncated), branch kept
		{"abc", "main", "release-2026.07", 14, "abc", "main", ""},
		// branch doesn't fit whole → truncated to fill, tag dropped
		{"abcdefgh", "feature-x", "v1", 16, "abcdefgh", "feat…", ""},
		// name alone exceeds width → truncated, no suffixes
		{"verylongname", "main", "v1", 6, "veryl…", "", ""},
		// EMPTY branch + a long tag must NOT truncate the tag (the reviewed bug):
		// the tag is dropped whole, never rendered partial.
		{"abc", "", "release-2026.07.03-rc1", 20, "abc", "", ""},
		// empty branch + a short tag shows the tag whole
		{"abc", "", "v1", 20, "abc", "", "v1"},
	}
	for _, c := range cases {
		n, b, tg := fitNameSuffixes(c.name, c.branch, c.tag, c.w)
		if n != c.wantN || b != c.wantB || tg != c.wantT {
			t.Errorf("fitNameSuffixes(%q,%q,%q,%d) = (%q,%q,%q), want (%q,%q,%q)",
				c.name, c.branch, c.tag, c.w, n, b, tg, c.wantN, c.wantB, c.wantT)
		}
	}
}

// t toggles showing each repo's latest tag inline in the Repos rows: off by
// default, on loads the tags, and the tag renders after the branch.
func TestTUI_TagsInlineToggle(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	if m.showTagsInline {
		t.Fatal("inline tags should be off by default")
	}

	// t turns it on and dispatches a load for every repo
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	if !m.showTagsInline {
		t.Fatal("t should turn on inline tags")
	}
	loads := 0
	for _, msg := range drainCmd(cmd) {
		if _, ok := msg.(latestTagMsg); ok {
			loads++
		}
	}
	if loads != len(m.repos) {
		t.Errorf("expected a tag load per repo (%d), got %d", len(m.repos), loads)
	}

	// a latestTagMsg populates that repo's tag, and it renders after the branch
	mm, _ = m.Update(latestTagMsg{path: m.repos[0].repo.Path, tag: "v1.2.3"})
	m = mm.(Model)
	if m.repos[0].latestTag != "v1.2.3" {
		t.Errorf("latestTag not stored: %q", m.repos[0].latestTag)
	}
	d := computeDims(100, 30, true)
	if body := stripANSI(m.renderRepoBody(d, 30)); !strings.Contains(body, "(v1.2.3)") {
		t.Errorf("the tag should render inline:\n%s", body)
	}

	// t again turns it off (and the tag no longer renders)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	if m.showTagsInline {
		t.Error("t should toggle inline tags off")
	}
	if body := stripANSI(m.renderRepoBody(d, 30)); strings.Contains(body, "(v1.2.3)") {
		t.Error("the tag should not render when inline tags are off")
	}
}

// n opens a full-screen news overlay listing every headline; j scrolls; n/esc
// closes.
func TestTUI_NewsView(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.newsFeed = []string{"a shipped X", "b fixed Y", "c released v2", "d refactored Z"}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = mm.(Model)
	if !m.showNews {
		t.Fatal("n should open the news overlay")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "a shipped X") || !strings.Contains(v, "d refactored Z") {
		t.Errorf("the news overlay should list every headline:\n%s", v)
	}

	// j scrolls, k scrolls back, clamped at 0
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = mm.(Model)
	if m.newsOffset != 1 {
		t.Errorf("j should scroll the news, offset=%d", m.newsOffset)
	}

	// esc closes
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.showNews {
		t.Error("esc should close the news overlay")
	}
}

// d/D arm a discard confirmation on the highlighted repo; nothing runs until y.
func TestTUI_DiscardConfirm(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	// make the highlighted repo look dirty so the discard isn't a no-op
	m.repos[0].status.DirtyCount = 2

	// D arms the full-clean confirmation, marked full
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	m = mm.(Model)
	if !m.confirmDiscard || !m.confirmDiscardFull || m.confirmDiscardPath != m.repos[0].repo.Path {
		t.Fatalf("D should arm a full discard for the current repo: %+v", m.confirmDiscard)
	}
	if cmd == nil {
		t.Error("arming should set a status prompt")
	}

	// a non-y key cancels without running anything
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = mm.(Model)
	if m.confirmDiscard {
		t.Error("any non-y key should cancel the discard")
	}

	// re-arm with lowercase d (tracked only), then y confirms → dispatches a command
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = mm.(Model)
	if !m.confirmDiscard || m.confirmDiscardFull {
		t.Fatal("d should arm a tracked-only discard")
	}
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = mm.(Model)
	if m.confirmDiscard {
		t.Error("y should disarm the confirmation")
	}
	if cmd == nil {
		t.Error("y should dispatch the discard command")
	}
}

// A discard on a repo known to be clean is a no-op (no confirmation armed).
func TestTUI_DiscardCleanNoop(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.repos[0].status.DirtyCount = 0 // clean (loadAll set loaded=true)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	m = mm.(Model)
	if m.confirmDiscard {
		t.Error("discarding a clean repo should not arm a confirmation")
	}
}

// Regaining terminal-window focus refetches every repo (like `r`).
func TestTUI_FocusRefetch(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	for _, r := range m.repos {
		r.fetching = false
	}
	mm, cmd := m.Update(tea.FocusMsg{})
	m = mm.(Model)
	if cmd == nil {
		t.Error("a focus event should trigger a refetch command")
	}
	for _, r := range m.repos {
		if !r.fetching {
			t.Error("focus refetch should mark every repo fetching")
		}
	}
}

// A focus event within the cooldown of the last fetch is ignored, so rapid
// alt-tabbing doesn't spray git fetches; once the cooldown lapses, it refetches.
func TestTUI_FocusRefetchCooldown(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	for _, r := range m.repos {
		r.fetching = false
	}
	// A fetch just happened → an immediate focus must be a no-op.
	m.lastFetch = time.Now()
	mm, cmd := m.Update(tea.FocusMsg{})
	m = mm.(Model)
	if cmd != nil {
		t.Error("a focus within the cooldown should not refetch")
	}
	for _, r := range m.repos {
		if r.fetching {
			t.Error("a focus within the cooldown must not mark repos fetching")
		}
	}
	// Once the cooldown has lapsed, focus refetches again.
	m.lastFetch = time.Now().Add(-2 * focusRefetchCooldown)
	mm, cmd = m.Update(tea.FocusMsg{})
	m = mm.(Model)
	if cmd == nil {
		t.Error("a focus after the cooldown should refetch")
	}
	for _, r := range m.repos {
		if !r.fetching {
			t.Error("a focus after the cooldown should mark every repo fetching")
		}
	}
}

// z maximizes the focused pane; zoom follows focus; z again restores the layout.
func TestTUI_Zoom(t *testing.T) {
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	mm, _ := m.Update(rk("z"))
	m = mm.(Model)
	if !m.zoomed {
		t.Fatal("z should zoom")
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "[1] Repos") || !strings.Contains(v, "zoom") {
		t.Errorf("zoom should show the focused Repos pane full-screen:\n%s", v)
	}
	if strings.Contains(v, "3 Branches") || strings.Contains(v, "[2] Scripts") {
		t.Error("zoom should show ONLY the focused pane")
	}
	// zoom follows focus
	mm, _ = m.Update(rk("5"))
	m = mm.(Model)
	if !m.zoomed {
		t.Error("switching focus should keep zoom on")
	}
	if !strings.Contains(stripANSI(m.View()), "Graph") {
		t.Error("zoomed bottom slot should show the Graph view")
	}
	// z restores the full layout
	mm, _ = m.Update(rk("z"))
	m = mm.(Model)
	if m.zoomed {
		t.Error("z should un-zoom")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "[1] Repos") || !strings.Contains(v, "3 Branches") {
		t.Error("restored view should show all panels again")
	}
}

// Action status messages set the status line and schedule an expiry; a matching
// expiry clears it, but a stale one (older generation) must not.
func TestTUI_StatusLineExpires(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	mm, cmd := m.Update(pushDoneMsg{path: m.repos[0].repo.Path})
	m = mm.(Model)
	if m.statusLine == "" {
		t.Fatal("expected a status line after push")
	}
	if cmd == nil {
		t.Fatal("expected an expiry command after push")
	}
	// matching expiry clears it
	mm, _ = m.Update(statusExpireMsg{gen: m.statusGen})
	if got := mm.(Model).statusLine; got != "" {
		t.Errorf("matching expire should clear the status, got %q", got)
	}
	// a stale expiry must not clear a newer message
	m.statusLine = "newer"
	m.statusGen = 5
	mm, _ = m.Update(statusExpireMsg{gen: 4})
	if mm.(Model).statusLine != "newer" {
		t.Error("stale expire should not clear a newer status")
	}
}

// F toggles a filter that shows only repos with changes / ahead / behind.
func TestTUI_AttentionFilter(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.repos[0].status = git.RepoStatus{HasUpstream: true, DirtyCount: 2} // needs attention
	m.repos[0].loaded = true
	m.repos[1].status = git.RepoStatus{HasUpstream: true} // clean & in sync
	m.repos[1].loaded = true

	if got := len(m.visibleRepos()); got != 2 {
		t.Fatalf("expected 2 visible without filter, got %d", got)
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	m = mm.(Model)
	vis := m.visibleRepos()
	if len(vis) != 1 || vis[0] != m.repos[0] {
		t.Errorf("attention filter should show only the dirty repo, got %d", len(vis))
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	if got := len(mm.(Model).visibleRepos()); got != 2 {
		t.Errorf("toggling filter off should restore all repos, got %d", got)
	}
}

func TestTUI_SyncSkipsDirtyRepo(t *testing.T) {
	cfg, repos := twoRepos(t)
	// make the first repo dirty
	if err := os.WriteFile(filepath.Join(repos[0].Path, "dirty.txt"), []byte("z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, New(cfg, "", repos, nil), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("*1"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("skipped"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTUI_ShowsBranchesForHighlighted(t *testing.T) {
	cfg, repos := twoRepos(t)
	gitCmd(t, repos[0].Path, "branch", "feature")
	tm := teatest.NewTestModel(t, New(cfg, "", repos, nil), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("feature")) && bytes.Contains(b, []byte("master"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTUI_ShowsLogForHighlighted(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, "", repos, nil), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("init"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// Regression guard for the Layout & Spacing Discipline: no rendered line may
// exceed the terminal width, at every supported width.
func TestTUI_LinesFitWidth(t *testing.T) {
	cfg, repos := twoRepos(t)
	for _, w := range []int{80, 100, 160, 200} {
		m := loadAll(t, New(cfg, "", repos, nil), w, 30)
		for _, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("w=%d: line width %d > %d: %q", w, got, w, line)
			}
		}
	}
}

// Repo rows must fit the left panel's content width at every supported terminal
// width — otherwise lipgloss wraps them and columns misalign. This guards the
// §6.1 fixed-width discipline at narrow widths (LinesFitWidth's single w=100 misses it).
func TestTUI_RepoRowsFitPanelContent(t *testing.T) {
	cfg, repos := twoRepos(t)
	for _, w := range []int{80, 81, 82, 83, 100, 160, 200} {
		m := loadAll(t, New(cfg, "", repos, nil), w, 30)
		d := computeDims(w, 30, false)
		content := d.leftW - 2 // panelStyle Padding(0,1) → content area is leftW-2
		for _, line := range strings.Split(m.renderRepoBody(d, 30), "\n") {
			if line == "" {
				continue
			}
			if got := lipgloss.Width(line); got > content {
				t.Errorf("w=%d: repo-body line width %d exceeds panel content %d: %q", w, got, content, line)
			}
		}
	}
}

// Decorative glyphs must be ASCII (guaranteed width-1). Ambiguous-East-Asian
// glyphs (▸ ● ↑ ↓ ⇕ ✓ ✔ …) render 2 cells wide in many terminals while
// lipgloss measures them as 1, which drifts columns — most visibly on the
// highlighted row. This guards against reintroducing them.
func TestTUI_DecorativeGlyphsAreASCII(t *testing.T) {
	states := []*repoVM{
		{loaded: false},
		{loaded: true, fetching: true},
		{loaded: true, status: git.RepoStatus{HasUpstream: false}},
		{loaded: true, status: git.RepoStatus{HasUpstream: true, Ahead: 2}},
		{loaded: true, status: git.RepoStatus{HasUpstream: true, Behind: 5}},
		{loaded: true, status: git.RepoStatus{HasUpstream: true, Ahead: 1, Behind: 3}},
		{loaded: true, status: git.RepoStatus{HasUpstream: true}},
	}
	for i, r := range states {
		// ascii mode (unicode=false) must be ASCII-safe.
		if g := stripANSI(syncGlyph(r, false)); !isASCII(g) {
			t.Errorf("syncGlyph[%d] = %q is not ASCII", i, g)
		}
	}
	if g := stripANSI(dirtyBadge(git.RepoStatus{DirtyCount: 3})); !isASCII(g) {
		t.Errorf("dirtyBadge = %q is not ASCII", g)
	}
	// cursor prefix via a rendered highlighted row
	cfg, repos := twoRepos(t)
	m := New(cfg, "", repos, nil)
	if row := stripANSI(m.renderRow(0, m.repos[0], 12)); !isASCII(row) {
		t.Errorf("renderRow (cursor row) = %q is not ASCII", row)
	}
}

// The panels must display their number so the focus keys are discoverable, and
// the bottom slot shows all three of its views (4/5/6) as a tab bar.
func TestTUI_PanelsShowNumbers(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	view := stripANSI(m.View())
	for _, want := range []string{"[1] Repos", "[2] Scripts", "3 Branches", "4 PRs", "5 Graph", "6 Changes", "7 Output"} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing panel label %q", want)
		}
	}
}

// The top-right slot advertises Branches (3) + PRs (4); the bottom slot advertises
// Graph (5) / Changes (6) / Output (7), marking a running Output with "*". Both
// use "│" dividers so every view is discoverable.
func TestTUI_TabBars(t *testing.T) {
	var m Model // topView/bottomView default to their zero values
	top := stripANSI(m.topTabs())
	for _, want := range []string{"3 Branches", "│", "4 PRs"} {
		if !strings.Contains(top, want) {
			t.Errorf("top tab bar %q missing %q", top, want)
		}
	}
	bottom := stripANSI(m.bottomTabs())
	for _, want := range []string{"5 Graph", "│", "6 Changes", "7 Output"} {
		if !strings.Contains(bottom, want) {
			t.Errorf("bottom tab bar %q missing %q", bottom, want)
		}
	}
	m.bottomView = bvOutput
	m.outputRunning = true
	if got := stripANSI(m.bottomTabs()); !strings.Contains(got, "7 Output*") {
		t.Errorf("running Output tab should show *: %q", got)
	}
}

// Right-column panel lines must be truncated to the panel content width so long
// branch names / graph lines / file paths can't wrap and grow the panel off-axis.
func TestTUI_PanelLinesFitContentWidth(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.branches = []git.Branch{{Name: strings.Repeat("b", 300)}}
	m.graphLines = []string{strings.Repeat("x", 300)}
	m.changeFiles = []git.FileChange{{Status: "M", Path: strings.Repeat("p", 300)}}
	content := computeDims(100, 30, false).rightW - 2
	blocks := []string{
		m.renderBranches(content, 10),
		m.renderGraphView(content, 10),
		m.renderChangesView(content, 10),
	}
	for _, block := range blocks {
		for _, line := range strings.Split(block, "\n") {
			if got := lipgloss.Width(line); got > content {
				t.Errorf("panel line width %d exceeds content %d: %q", got, content, line)
			}
		}
	}
}

// In the Branches panel, j/k must move the branch cursor — NOT the repo cursor
// (which would reload the panels and make branches "change on every keystroke").
func TestTUI_BranchNavIsPanelScoped(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.branches = []git.Branch{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	m.focus = panelBranches
	startRepo := m.cursor
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(Model)
	if m.branchCursor != 1 {
		t.Errorf("branchCursor after j in Branches panel = %d, want 1", m.branchCursor)
	}
	if m.cursor != startRepo {
		t.Errorf("repo cursor moved (%d→%d) while browsing branches", startRepo, m.cursor)
	}
}

// syncGlyph renders ↑/↓ in unicode mode and alignment-safe +/- in ascii mode.
func TestTUI_SyncGlyphModes(t *testing.T) {
	ahead := &repoVM{loaded: true, status: git.RepoStatus{HasUpstream: true, Ahead: 2}}
	behind := &repoVM{loaded: true, status: git.RepoStatus{HasUpstream: true, Behind: 8}}
	cases := []struct {
		vm      *repoVM
		unicode bool
		want    string
	}{
		{ahead, false, "+2"}, {behind, false, "-8"},
		{ahead, true, "↑2"}, {behind, true, "↓8"},
	}
	for _, c := range cases {
		if g := stripANSI(syncGlyph(c.vm, c.unicode)); g != c.want {
			t.Errorf("syncGlyph(unicode=%v) = %q, want %q", c.unicode, g, c.want)
		}
	}
}

// A local-only repo (no remote configured) reads "no-remote", not the red "!"
// reserved for a genuine error or an unpushed branch — and s/p skip it with a
// reason instead of failing with git's "No configured push destination".
func TestTUI_NoRemoteRepo(t *testing.T) {
	local := &repoVM{loaded: true, status: git.RepoStatus{}}                   // no remote at all
	unpushed := &repoVM{loaded: true, status: git.RepoStatus{HasRemote: true}} // remote, branch not pushed
	synced := &repoVM{loaded: true, status: git.RepoStatus{HasRemote: true, HasUpstream: true}}
	for _, c := range []struct {
		vm   *repoVM
		want string
	}{
		{local, "no-remote"}, {unpushed, "!"}, {synced, "ok"},
	} {
		if g := stripANSI(syncGlyph(c.vm, false)); g != c.want {
			t.Errorf("syncGlyph = %q, want %q", g, c.want)
		}
	}

	// twoRepos builds plain `git init` repos — local-only, so s and p must skip.
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	if !strings.Contains(stripANSI(m.View()), "no-remote") {
		t.Error("the Repos panel should mark a local-only repo no-remote")
	}
	for key, want := range map[string]string{"s": "sync", "p": "push"} {
		mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cmd == nil {
			t.Fatalf("%q should produce a command", key)
		}
		mm, _ = mm.(Model).Update(cmd())
		got := stripANSI(mm.(Model).statusLine)
		if !strings.Contains(got, want+" ") || !strings.Contains(got, "skipped: no remote") {
			t.Errorf("%q on a local-only repo: status = %q, want %q ... skipped: no remote", key, got, want)
		}
	}
}

// ? opens the KEYBINDINGS — it is the universal "show me the keys" reflex, so it
// must not land on a settings form. , opens settings; tab flips between the two;
// esc closes.
func TestTUI_HelpOverlay(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = mm.(Model)
	if !m.showHelp || !m.showKeys {
		t.Fatalf("? should open the keybindings; showHelp=%v showKeys=%v", m.showHelp, m.showKeys)
	}
	v := stripANSI(m.View())
	for _, want := range []string{"PUSH", "PULL", "dirty", "sync", "branches"} {
		if !strings.Contains(v, want) {
			t.Errorf("keys view missing %q", want)
		}
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.(Model).showHelp {
		t.Error("esc should close the overlay")
	}
}

// --- GitHub PR pane -------------------------------------------------------

// key 4 focuses the PRs view (beside Branches); `m` toggles mine <->
// review-requested (resetting the cursor), and the rows show the PR + its author.
func TestTUI_PRPaneToggle(t *testing.T) {
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghAvailable, m.ghUser = true, true, "rabeeh-ta"
	m.prLoaded = true
	m.prMine = []gh.PullRequest{{Number: 1, Title: "mine one", Author: "rabeeh-ta", RepoSlug: "o/a"}}
	m.prReview = []gh.PullRequest{
		{Number: 2, Title: "rev two", Author: "bob", RepoSlug: "o/b"},
		{Number: 3, Title: "rev three", Author: "amy", RepoSlug: "o/c"},
	}

	mm, _ := m.Update(rk("4"))
	m = mm.(Model)
	if m.focus != panelBranches || m.topView != tvPRs {
		t.Fatal("4 should focus the Branches panel on its PRs view")
	}
	if m.prShowReview {
		t.Fatal("the PR pane should start on the 'mine' list")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "mine one") {
		t.Errorf("PR pane should list my PR; view:\n%s", v)
	}

	m.prCursor = 1 // will reset on toggle
	mm, _ = m.Update(rk("m"))
	m = mm.(Model)
	if !m.prShowReview || m.prCursor != 0 {
		t.Fatalf("m should switch to the review list and reset the cursor, showReview=%v cursor=%d", m.prShowReview, m.prCursor)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "rev two") || !strings.Contains(v, "@bob") {
		t.Errorf("review list should show the PR and its author; view:\n%s", v)
	}

	mm, _ = m.Update(rk("m"))
	if mm.(Model).prShowReview {
		t.Fatal("m should toggle back to the 'mine' list")
	}

	// m outside the PR pane does nothing.
	m2 := loadAll(t, New(cfg, "", repos, nil), 120, 40) // focus starts on Repos
	m2.prShowReview = false
	mm, _ = m2.Update(rk("m"))
	if got := mm.(Model); got.prShowReview || got.focus != panelRepos {
		t.Error("m outside the PR pane must be a no-op")
	}
}

// / in the PR pane filters the PR list only (not the repos), and leaving the pane
// clears that filter.
func TestTUI_PRFilterScoped(t *testing.T) {
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghAvailable = true, true
	m.prLoaded = true
	m.prMine = []gh.PullRequest{
		{Number: 1, Title: "add authoring tab", Author: "a", RepoSlug: "o/authoring"},
		{Number: 2, Title: "fix account bug", Author: "b", RepoSlug: "o/account"},
	}

	mm, _ := m.Update(rk("4"))
	m = mm.(Model)
	mm, _ = m.Update(rk("/"))
	m = mm.(Model)
	if m.filterPanel != filterPRs {
		t.Fatalf("/ in the PR pane should scope the filter to filterPRs, got %v", m.filterPanel)
	}
	for _, r := range "account" {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(Model)
	}
	if vis := m.visiblePRs(); len(vis) != 1 || vis[0].Number != 2 {
		t.Fatalf("filter 'account' should match PR #2 only, got %d PRs", len(vis))
	}
	if len(m.visibleRepos()) != len(m.repos) {
		t.Error("a PR-scoped filter must not narrow the repos list")
	}

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit the filter
	m = mm.(Model)
	mm, _ = m.Update(rk("5")) // switch to Graph
	m = mm.(Model)
	if m.filterPanel == filterPRs || m.filter != "" {
		t.Errorf("leaving the PR view should clear its filter, panel=%v filter=%q", m.filterPanel, m.filter)
	}
}

// j/k move the PR cursor within the visible list (clamped at both ends).
func TestTUI_PRNav(t *testing.T) {
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghAvailable, m.prLoaded = true, true, true
	m.prMine = []gh.PullRequest{{Number: 1}, {Number: 2}, {Number: 3}}
	mm, _ := m.Update(rk("4"))
	m = mm.(Model)
	for _, want := range []int{1, 2, 2} { // down, down, down (clamped at 2)
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = mm.(Model)
		if m.prCursor != want {
			t.Fatalf("prCursor = %d, want %d", m.prCursor, want)
		}
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if mm.(Model).prCursor != 1 {
		t.Fatalf("k should move the PR cursor up to 1, got %d", mm.(Model).prCursor)
	}
}

// enter on a PR checks out the matching local clone when it's clean; it explains
// itself when the repo isn't present or the tree is dirty. The gh command is never
// executed here (we only assert the decision + status).
func TestTUI_PRCheckoutDecision(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghAvailable, m.prLoaded = true, true, true
	m.focus, m.topView = panelBranches, tvPRs
	m.repos[0].status.Slug = "acme/widgets"
	m.repos[0].status.DirtyCount = 0

	// matched + clean → a checkout is kicked off.
	m.prMine = []gh.PullRequest{{Number: 7, RepoSlug: "acme/widgets"}}
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := stripANSI(mm.(Model).statusLine); !strings.Contains(got, "checking out PR #7") || cmd == nil {
		t.Fatalf("clean matched checkout: status=%q cmd=%v", got, cmd)
	}

	// dirty tree → skipped.
	m.repos[0].status.DirtyCount = 3
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := stripANSI(mm.(Model).statusLine); !strings.Contains(got, "dirty working tree") {
		t.Errorf("dirty checkout should skip: status=%q", got)
	}

	// no matching local clone → not in view.
	m.repos[0].status.DirtyCount = 0
	m.prMine = []gh.PullRequest{{Number: 9, RepoSlug: "nope/here"}}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := stripANSI(mm.(Model).statusLine); !strings.Contains(got, "not in view") {
		t.Errorf("unmatched PR should say not in view: status=%q", got)
	}
}

// After a SUCCESSFUL PR checkout the app lands on that repo's Branches sub-view
// (topView reset to tvBranches), not the PR list — ready to review. A FAILED
// checkout leaves you on the PRs view with an error.
func TestTUI_PRCheckoutLandsOnBranches(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.focus, m.topView, m.prShowReview, m.prCursor = panelBranches, tvPRs, true, 3
	target := m.repos[1].repo.Path

	mm, _ := m.Update(prCheckoutDoneMsg{path: target, number: 42, err: nil})
	got := mm.(Model)
	if got.focus != panelBranches || got.topView != tvBranches {
		t.Fatalf("checkout should land on the Branches sub-view, got focus=%v topView=%v", got.focus, got.topView)
	}
	if got.cursor != 1 {
		t.Errorf("cursor should move to the checked-out repo (index 1), got %d", got.cursor)
	}
	if s := stripANSI(got.statusLine); !strings.Contains(s, "checked out PR #42") {
		t.Errorf("status should confirm the checkout, got %q", s)
	}

	// a failed checkout stays on the PRs view with an error status.
	m2 := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m2.focus, m2.topView = panelBranches, tvPRs
	mm, _ = m2.Update(prCheckoutDoneMsg{path: target, number: 42, err: fmt.Errorf("boom")})
	if g := mm.(Model); g.topView != tvPRs || !strings.Contains(stripANSI(g.statusLine), "failed") {
		t.Errorf("failed checkout should stay on PRs with an error, topView=%v status=%q", g.topView, stripANSI(g.statusLine))
	}
}

// The top-bar badge and bottom-bar github indicator appear only when gh is
// available; both vanish without it.
func TestTUI_PRBadgeAndIndicator(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghAvailable, m.ghUser = true, true, "rabeeh-ta"
	m.prReview = make([]gh.PullRequest, 2)
	m.prMine = make([]gh.PullRequest, 1)
	v := stripANSI(m.View())
	for _, want := range []string{"review 2", "mine 1", "github: rabeeh-ta"} {
		if !strings.Contains(v, want) {
			t.Errorf("View missing %q:\n%s", want, v)
		}
	}
	m.ghAvailable = false
	if v := stripANSI(m.View()); strings.Contains(v, "github:") || strings.Contains(v, "review 2") {
		t.Error("without gh, the badge and github indicator must be hidden")
	}
}

// The PR pane hints why it's empty when gh isn't usable.
func TestTUI_PRUnavailableHint(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghInstalled, m.ghAvailable = true, false, false
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	if v := stripANSI(mm.(Model).View()); !strings.Contains(v, "gh not installed") {
		t.Errorf("PR pane should hint that gh is not installed; view:\n%s", v)
	}
}

// An available probe loads both PR lists; prsMsg fills the right one. An
// unavailable probe loads nothing. (The load commands are not executed here.)
func TestTUI_GhProbeAndPrsMsg(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	mm, cmd := m.Update(ghProbeMsg{installed: true, available: true, user: "u"})
	m = mm.(Model)
	if !m.ghAvailable || m.ghUser != "u" || cmd == nil {
		t.Fatalf("available probe should set the user and return a load cmd: user=%q cmd=%v", m.ghUser, cmd)
	}
	mm, _ = m.Update(prsMsg{review: true, prs: []gh.PullRequest{{Number: 5}}})
	m = mm.(Model)
	if len(m.prReview) != 1 || !m.prLoaded {
		t.Fatal("prsMsg(review) should fill prReview and set prLoaded")
	}
	mm, _ = m.Update(prsMsg{review: false, prs: []gh.PullRequest{{Number: 6}, {Number: 7}}})
	if len(mm.(Model).prMine) != 2 {
		t.Fatal("prsMsg(mine) should fill prMine")
	}

	_, cmd2 := loadAll(t, New(cfg, "", repos, nil), 120, 40).Update(ghProbeMsg{})
	if cmd2 != nil {
		t.Error("an unavailable probe should not load PRs")
	}
}

// `o` now surfaces a failure (missing command / editor CLI refusing) as a status
// line instead of failing silently; a command that launches leaves no error.
func TestTUI_OpenSurfacesError(t *testing.T) {
	// a command that can't start -> error carried back...
	msg := openRepoCmd("definitely-not-a-real-editor-xyz-123", t.TempDir())()
	od, ok := msg.(openDoneMsg)
	if !ok || od.err == nil {
		t.Fatalf("a missing editor command should return an error, got %#v", msg)
	}
	// ...and the handler renders it into the status line.
	var m Model
	mm, _ := m.Update(od)
	if s := stripANSI(mm.(Model).statusLine); !strings.Contains(s, "open") || !strings.Contains(s, "failed") {
		t.Errorf("open error should show in the status line, got %q", s)
	}

	// a command that exits 0 with NO output is treated as launched -> no error.
	if od := openRepoCmd("true", t.TempDir())().(openDoneMsg); od.err != nil {
		t.Errorf("a clean silent launch should carry no error, got %v", od.err)
	}
	// a command that exits non-zero surfaces an error.
	if od := openRepoCmd("false", t.TempDir())().(openDoneMsg); od.err == nil {
		t.Error("a non-zero exit should surface an error")
	}
	// the key case: an editor CLI that prints a message but still exits 0 (VS Code
	// over plain SSH) must surface that message as an error.
	warn := filepath.Join(t.TempDir(), "warn.sh")
	if err := os.WriteFile(warn, []byte("#!/bin/sh\necho 'only available inside a Visual Studio Code terminal'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if od := openRepoCmd(warn, t.TempDir())().(openDoneMsg); od.err == nil || !strings.Contains(od.err.Error(), "Visual Studio Code terminal") {
		t.Errorf("output-on-success should surface as an error, got %v", od.err)
	}
}

// A plain SSH shell (iTerm/Terminal) has no VSCODE_IPC_HOOK_CLI, so the editor CLI
// can't reach the editor. liveEditorSocket finds a live socket itself — skipping
// stale ones left behind by closed windows, even when a stale one is NEWEST.
func TestEditorSocketDiscovery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("VSCODE_IPC_HOOK_CLI", "") // simulate a plain SSH shell

	// no sockets at all -> nothing to inject
	if got := editorSocketEnv(); got != "" {
		t.Errorf("with no sockets editorSocketEnv should be empty, got %q", got)
	}

	// a LIVE socket (real listener), created first so it is the OLDER one
	live := filepath.Join(dir, "vscode-ipc-live.sock")
	ln, err := net.Listen("unix", live)
	if err != nil {
		t.Skipf("cannot create a unix socket here: %v", err)
	}
	defer ln.Close()

	// a STALE socket file, created last so it sorts NEWEST and is tried first
	time.Sleep(10 * time.Millisecond)
	stale := filepath.Join(dir, "vscode-ipc-stale.sock")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := liveEditorSocket(); got != live {
		t.Errorf("should skip the newest STALE socket and pick the live one\n got  %q\n want %q", got, live)
	}
	if got, want := editorSocketEnv(), "VSCODE_IPC_HOOK_CLI="+live; got != want {
		t.Errorf("editorSocketEnv = %q, want %q", got, want)
	}

	// inside an editor terminal the var is already set -> leave the env alone
	t.Setenv("VSCODE_IPC_HOOK_CLI", "/already/set.sock")
	if got := editorSocketEnv(); got != "" {
		t.Errorf("with the hook already set editorSocketEnv should be empty, got %q", got)
	}
}

// The open command is whatever you'd type in the repo: "code", "code .",
// "cursor .", "code -r" — manygit supplies the folder. A real program path (even
// with spaces) is used verbatim rather than split.
func TestOpenArgs(t *testing.T) {
	// a real program whose path contains a space must NOT be word-split
	dir := filepath.Join(t.TempDir(), "my editor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(dir, "ed")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, a, ok := openArgs(real, "/repo"); !ok || p != real || !slices.Equal(a, []string{"/repo"}) {
		t.Errorf("a real program path with spaces should be verbatim, got (%q, %v, %v)", p, a, ok)
	}

	for _, c := range []struct {
		in       string
		wantProg string
		wantArgs []string
		wantOK   bool
	}{
		{"code", "code", []string{"/repo"}, true},
		{"code .", "code", []string{"/repo"}, true}, // the classic "code ." case
		{"cursor .", "cursor", []string{"/repo"}, true},
		{"code -r", "code", []string{"-r", "/repo"}, true}, // flags preserved
		{"code -r .", "code", []string{"-r", "/repo"}, true},
		{"  code  ", "code", []string{"/repo"}, true},
		{"", "", nil, false},
		{"   ", "", nil, false},
	} {
		p, a, ok := openArgs(c.in, "/repo")
		if ok != c.wantOK || p != c.wantProg || !slices.Equal(a, c.wantArgs) {
			t.Errorf("openArgs(%q) = (%q, %v, %v), want (%q, %v, %v)", c.in, p, a, ok, c.wantProg, c.wantArgs, c.wantOK)
		}
	}
}
