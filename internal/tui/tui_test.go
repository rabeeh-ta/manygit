package tui

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
	tm := teatest.NewTestModel(t, New(cfg, repos), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("alpha")) && bytes.Contains(b, []byte("bravo"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTUI_CursorMovesDown(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, repos), teatest.WithInitialTermSize(120, 40))
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

func TestTUI_SpaceSelectsRepo(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, repos), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("alpha"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("✔"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTUI_FilterNarrowsList(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, repos), teatest.WithInitialTermSize(120, 40))
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

func TestTUI_SyncSkipsDirtyRepo(t *testing.T) {
	cfg, repos := twoRepos(t)
	// make the first repo dirty
	if err := os.WriteFile(filepath.Join(repos[0].Path, "dirty.txt"), []byte("z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, New(cfg, repos), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("●1"))
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
	tm := teatest.NewTestModel(t, New(cfg, repos), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("feature")) && bytes.Contains(b, []byte("master"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTUI_ShowsLogForHighlighted(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, repos), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("init"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// Regression guard for the Layout & Spacing Discipline: no rendered line may
// exceed the terminal width.
func TestTUI_LinesFitWidth(t *testing.T) {
	cfg, repos := twoRepos(t)
	const w = 100
	m := loadAll(t, New(cfg, repos), w, 30)
	for _, line := range strings.Split(m.View(), "\n") {
		if lipgloss.Width(line) > w {
			t.Errorf("line width %d > %d: %q", lipgloss.Width(line), w, line)
		}
	}
}
