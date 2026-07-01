package tui

import (
	"bytes"
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
		for _, line := range strings.Split(m.renderRepoBody(d), "\n") {
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

// The four panels must display their number so the 1/2/3/4 focus keys are
// discoverable.
func TestTUI_PanelsShowNumbers(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 120, 40)
	view := stripANSI(m.View())
	for _, want := range []string{"[1] Repos", "[2] Branches", "[3] Log", "[4] Scripts"} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing panel label %q", want)
		}
	}
}

// Branch and log panel lines must be truncated to the panel content width so
// long branch names / commit lines can't wrap and grow the panel off-axis.
func TestTUI_PanelLinesFitContentWidth(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	m.branches = []git.Branch{{Name: strings.Repeat("b", 300)}}
	m.log = []string{strings.Repeat("x", 300)}
	content := computeDims(100, 30).rightW - 2
	for _, block := range []string{m.renderBranches(content), m.renderLog(content)} {
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

// ? opens a help overlay explaining the status glyphs and keys; any other key closes it.
func TestTUI_HelpOverlay(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = mm.(Model)
	if !m.showHelp {
		t.Fatal("? should open the help overlay")
	}
	v := stripANSI(m.View())
	for _, want := range []string{"PUSH", "PULL", "dirty", "sync", "branches"} {
		if !strings.Contains(v, want) {
			t.Errorf("help overlay missing %q", want)
		}
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.(Model).showHelp {
		t.Error("esc should close help")
	}
}
