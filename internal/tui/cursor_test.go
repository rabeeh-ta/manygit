package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/git"
)

func cursorKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// attentionModel returns a loaded model with both repos needing attention and
// the `F` filter on, cursor parked on the LAST visible row — the setup where
// acting on that repo makes it leave the list under the cursor.
func attentionModel(t *testing.T) (Model, *repoVM) {
	t.Helper()
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	for _, r := range m.repos {
		st := r.status
		st.Ahead = 1 // something to push, so F keeps them
		r.status = st
	}
	mm, _ := m.Update(cursorKey("F"))
	m = mm.(Model)
	if len(m.visibleRepos()) != 2 {
		t.Fatalf("both repos should need attention, got %d visible", len(m.visibleRepos()))
	}
	mm, _ = m.Update(cursorKey("j")) // onto the last row
	m = mm.(Model)
	last := m.currentVisible(m.visibleRepos())
	if last == nil {
		t.Fatal("cursor should be on the second repo")
	}
	return m, last
}

// cleanStatus is what a successful push reports back for a repo: nothing ahead,
// nothing dirty — i.e. it no longer needs attention, so `F` stops showing it.
func cleanStatus(r *repoVM) git.RepoStatus {
	return git.RepoStatus{Branch: r.status.Branch, HasRemote: true}
}

// Pushing the highlighted repo under `F` makes its row leave the list. The
// cursor is an index into that list, so without a re-clamp it dangles past the
// end: nothing is highlighted and every repo action silently no-ops, because
// currentVisible returns nil. This is the bug that shipped.
func TestTUI_StatusChangeNeverStrandsCursorPastEnd(t *testing.T) {
	m, last := attentionModel(t)

	mm, _ := m.Update(statusMsg{path: last.repo.Path, st: cleanStatus(last)})
	m = mm.(Model)

	vis := m.visibleRepos()
	if len(vis) != 1 {
		t.Fatalf("the pushed repo should have left the F list, got %d visible", len(vis))
	}
	if m.cursor >= len(vis) {
		t.Fatalf("cursor %d dangles past the %d visible rows", m.cursor, len(vis))
	}
	if m.currentVisible(vis) == nil {
		t.Fatal("no row highlighted: s/p/d/o would all be silent no-ops")
	}
}

// When the row under the cursor leaves, the cursor lands on whatever took its
// place — a different repo. The panels MUST reload, or they keep rendering the
// departed repo's branches while s/p/d/enter act on the new one. That mismatch
// is the dangerous half of the bug: a checkout could land in the wrong repo.
func TestTUI_StatusChangeReloadsPanelsWhenCursorLandsElsewhere(t *testing.T) {
	m, last := attentionModel(t)

	mm, cmd := m.Update(statusMsg{path: last.repo.Path, st: cleanStatus(last)})
	m = mm.(Model)

	cur := m.currentVisible(m.visibleRepos())
	if cur == nil {
		t.Fatal("expected the cursor to land on the surviving repo")
	}
	if cur.repo.Path == last.repo.Path {
		t.Fatal("the pushed repo should no longer be under the cursor")
	}
	if cmd == nil {
		t.Errorf("cursor moved %s -> %s but the panels were not reloaded",
			last.repo.Name, cur.repo.Name)
	}
}

// The guard above must not fire on every statusMsg. Init delivers one per repo,
// and a needless loadContext resets graphSel/graphOffset/changeShowDiff — it
// would collapse an open diff every time any repo's status landed.
func TestTUI_StatusChangeDoesNotReloadWhenCursorStays(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)

	// No filter: the list can't shrink, so the cursor cannot move.
	for _, r := range m.repos {
		st := r.status
		st.Ahead = 2
		mm, cmd := m.Update(statusMsg{path: r.repo.Path, st: st})
		m = mm.(Model)
		if cmd != nil {
			t.Fatalf("statusMsg for %s reloaded the panels though the cursor never moved",
				r.repo.Name)
		}
	}
}
