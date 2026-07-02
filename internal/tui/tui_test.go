package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"

	"manygit/internal/config"
	"manygit/internal/discover"
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
	return cfg, repos
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
	tm := teatest.NewTestModel(t, New(cfg, repos, nil), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("alpha")) && bytes.Contains(b, []byte("bravo"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTUI_CursorMovesDown(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, repos, nil), teatest.WithInitialTermSize(120, 40))
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

// space drills into the highlighted repo's branches (and toggles back), instead
// of the old multi-select.
func TestTUI_SpaceFocusesBranches(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30) // focus starts on Repos
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	m = mm.(Model)
	if m.focus != panelBranches {
		t.Errorf("space in Repos panel should focus Branches, got %v", m.focus)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if mm.(Model).focus != panelRepos {
		t.Error("space in Branches panel should return to Repos")
	}
}

func TestTUI_FilterNarrowsList(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, repos, nil), teatest.WithInitialTermSize(120, 40))
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

// The Scripts panel lists discovered scripts; j/k move its cursor and space
// (when it's focused) builds a run command for the highlighted script.
func TestTUI_ScriptsPanel(t *testing.T) {
	cfg, repos := twoRepos(t)
	scripts := []discover.Script{
		{Path: "/x/a.sh", Name: "a.sh"},
		{Path: "/x/scripts/b.sh", Name: "scripts/b.sh"},
	}
	m := loadAll(t, New(cfg, repos, scripts), 100, 30)
	m.focus = panelScripts

	if v := stripANSI(m.View()); !strings.Contains(v, "a.sh") || !strings.Contains(v, "scripts/b.sh") {
		t.Errorf("Scripts panel should render script names")
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(Model)
	if m.scriptCursor != 1 {
		t.Errorf("j in Scripts panel should move scriptCursor, got %d", m.scriptCursor)
	}
	// space in the Scripts panel yields a (non-nil) run command; it does not run here.
	if m.runScriptCmd() == nil {
		t.Errorf("expected a run command for the highlighted script")
	}
	// with no scripts, run is a no-op.
	empty := loadAll(t, New(cfg, repos, nil), 100, 30)
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
	m := loadAll(t, New(cfg, repos, scripts), 100, 30)
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
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	m = mm.(Model)
	if m.outputTitle != "sync-edx.sh" {
		t.Errorf("space should run the highlighted filtered script, got %q", m.outputTitle)
	}
}

// Long ref names in the graph decorations are shortened when the graph loads,
// leaving the commit subject intact.
func TestTUI_GraphRefsShortened(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
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
	m := loadAll(t, New(cfg, repos, nil), 120, 30)
	d := computeDims(120, 30)
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
	m := loadAll(t, New(cfg, repos, nil), 120, 40)
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
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
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
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	path := m.currentVisible(m.visibleRepos()).repo.Path
	mm, _ := m.Update(graphMsg{path: path, lines: []string{"* aaaaaaa fix", "* bbbbbbb add"},
		commits: []git.GraphEntry{{Line: 0, Hash: "aaaaaaa"}, {Line: 1, Hash: "bbbbbbb"}}})
	m = mm.(Model)

	mm, _ = m.Update(rk("4"))
	m = mm.(Model)
	if m.focus != panelBottom || m.bottomView != bvGraph {
		t.Fatal("4 should focus the graph view")
	}
	if m.selectedRef() != "" {
		t.Errorf("WIP ref should be empty, got %q", m.selectedRef())
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // WIP -> first commit
	m = mm.(Model)
	if m.graphSel != 1 || m.selectedRef() != "aaaaaaa" {
		t.Errorf("j should select aaaaaaa, sel=%d ref=%q", m.graphSel, m.selectedRef())
	}
	mm, cmd := m.Update(rk("5"))
	m = mm.(Model)
	if m.bottomView != bvChanges || cmd == nil {
		t.Fatal("5 should switch to Changes and load the selection's files")
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
	mm, _ = m.Update(rk("6"))
	if mm.(Model).bottomView != bvOutput {
		t.Error("6 should switch to Output")
	}
}

// From the Graph, enter drills into the selected commit's changed files; enter on
// a file opens its diff; esc walks back diff → files → graph.
func TestTUI_GraphDrillDown(t *testing.T) {
	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	path := m.currentVisible(m.visibleRepos()).repo.Path
	mm, _ := m.Update(graphMsg{path: path, lines: []string{"* aaaaaaa first"},
		commits: []git.GraphEntry{{Line: 0, Hash: "aaaaaaa"}}})
	m = mm.(Model)

	mm, _ = m.Update(rk("4")) // Graph
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
	m := New(config.Default(), repos, nil)
	m.width, m.height = 100, 20
	d := computeDims(100, 20)

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
	m := loadAll(t, New(cfg, repos, nil), 100, 30)

	// open Changes (5), then focus Repos (1)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("5")})
	m = mm.(Model)
	if m.bottomView != bvChanges {
		t.Fatal("5 should show the Changes view")
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

// Regaining terminal-window focus refetches every repo (like `r`).
func TestTUI_FocusRefetch(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
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
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
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
	m := loadAll(t, New(cfg, repos, nil), 100, 30)

	mm, _ := m.Update(rk("z"))
	m = mm.(Model)
	if !m.zoomed {
		t.Fatal("z should zoom")
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "[1] Repos") || !strings.Contains(v, "zoom") {
		t.Errorf("zoom should show the focused Repos pane full-screen:\n%s", v)
	}
	if strings.Contains(v, "[3] Branches") || strings.Contains(v, "[2] Scripts") {
		t.Error("zoom should show ONLY the focused pane")
	}
	// zoom follows focus
	mm, _ = m.Update(rk("4"))
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
	if v := stripANSI(m.View()); !strings.Contains(v, "[1] Repos") || !strings.Contains(v, "[3] Branches") {
		t.Error("restored view should show all panels again")
	}
}

// Action status messages set the status line and schedule an expiry; a matching
// expiry clears it, but a stale one (older generation) must not.
func TestTUI_StatusLineExpires(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)

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
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
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
	tm := teatest.NewTestModel(t, New(cfg, repos, nil), teatest.WithInitialTermSize(120, 40))
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
	tm := teatest.NewTestModel(t, New(cfg, repos, nil), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("feature")) && bytes.Contains(b, []byte("master"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTUI_ShowsLogForHighlighted(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, repos, nil), teatest.WithInitialTermSize(120, 40))
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
		m := loadAll(t, New(cfg, repos, nil), w, 30)
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
		m := loadAll(t, New(cfg, repos, nil), w, 30)
		d := computeDims(w, 30)
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
	m := New(cfg, repos, nil)
	if row := stripANSI(m.renderRow(0, m.repos[0], 12)); !isASCII(row) {
		t.Errorf("renderRow (cursor row) = %q is not ASCII", row)
	}
}

// The panels must display their number so the focus keys are discoverable, and
// the bottom slot shows all three of its views (4/5/6) as a tab bar.
func TestTUI_PanelsShowNumbers(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 120, 40)
	view := stripANSI(m.View())
	for _, want := range []string{"[1] Repos", "[2] Scripts", "[3] Branches", "4 Graph", "5 Changes", "6 Output"} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing panel label %q", want)
		}
	}
}

// The bottom tab bar lists all three views separated by "│" dividers and marks a
// running Output with "*", so the views are distinct and always advertised.
func TestTUI_BottomTabBar(t *testing.T) {
	var m Model // bottomView defaults to bvGraph
	plain := stripANSI(m.bottomTabs())
	for _, want := range []string{"4 Graph", "│", "5 Changes", "6 Output"} {
		if !strings.Contains(plain, want) {
			t.Errorf("tab bar %q missing %q", plain, want)
		}
	}
	m.bottomView = bvOutput
	m.outputRunning = true
	if got := stripANSI(m.bottomTabs()); !strings.Contains(got, "6 Output*") {
		t.Errorf("running Output tab should show *: %q", got)
	}
}

// Right-column panel lines must be truncated to the panel content width so long
// branch names / graph lines / file paths can't wrap and grow the panel off-axis.
func TestTUI_PanelLinesFitContentWidth(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	m.branches = []git.Branch{{Name: strings.Repeat("b", 300)}}
	m.graphLines = []string{strings.Repeat("x", 300)}
	m.changeFiles = []git.FileChange{{Status: "M", Path: strings.Repeat("p", 300)}}
	content := computeDims(100, 30).rightW - 2
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
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
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

// ? opens the settings overlay; tab flips to the keybindings/legend view; esc closes.
func TestTUI_HelpOverlay(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = mm.(Model)
	if !m.showHelp {
		t.Fatal("? should open the settings overlay")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // flip to the keybindings view
	m = mm.(Model)
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
