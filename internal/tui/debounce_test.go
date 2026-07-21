package tui

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// sweepModel is a Model holding n loaded repos with the cursor at the top — the
// setup a user is in when they hold j to run down a long repo list.
func sweepModel(n int) Model {
	vms := make([]*repoVM, n)
	for i := range vms {
		vms[i] = vm(fmt.Sprintf("repo%02d", i), "grp", "master", "")
	}
	return Model{repos: vms, focus: panelRepos, width: 120, height: 40}
}

// Holding j down a 30-repo list must not load context once per row. Each load is
// 2-3 git subprocesses — branches plus a `git log --graph --all` that walks every
// ref — so a full sweep spawns ~60-90 processes, all but the last discarded as
// stale by the path guard in Update. Only moves that land after a quiet gap
// should load; the rest collapse into one trailing load.
func TestTUI_RepoSweepDebouncesContextLoads(t *testing.T) {
	m := sweepModel(30)

	loads := 0
	for i := 0; i < 29; i++ { // j down every row, as fast as key repeat delivers
		mm, _ := m.Update(cursorKey("j"))
		m = mm.(Model)
		if !m.ctxPending {
			loads++
		}
	}

	if loads > 2 {
		t.Fatalf("a 30-row sweep loaded context %d times; want at most 2 (one leading, one trailing)", loads)
	}
	if !m.ctxPending {
		t.Fatal("after a sweep a trailing load should be scheduled")
	}
}

// A deliberate press — one j after a pause — must load immediately. The debounce
// exists to collapse key-repeat sweeps, not to add latency to ordinary
// navigation: stopping on a repo should still feel instant.
func TestTUI_DeliberateMoveLoadsContextImmediately(t *testing.T) {
	m := sweepModel(30)

	mm, cmd := m.Update(cursorKey("j"))
	m = mm.(Model)
	if m.ctxPending {
		t.Fatal("the first move after a quiet period must load immediately, not defer")
	}
	if cmd == nil {
		t.Fatal("an immediate move should return a context-load command")
	}

	time.Sleep(ctxSettle + 20*time.Millisecond) // the user pauses to read this repo

	mm, cmd = m.Update(cursorKey("j"))
	m = mm.(Model)
	if m.ctxPending {
		t.Fatal("a move after the settle window must load immediately, not defer")
	}
	if cmd == nil {
		t.Fatal("an immediate move should return a context-load command")
	}
}

// Only the newest scheduled load may survive. Bubble Tea cannot cancel a pending
// tea.Tick, so superseded ticks have to be dropped by generation (the
// newsDebounce idiom) — otherwise every row's load still lands, just later.
func TestTUI_SupersededContextDebounceIsDropped(t *testing.T) {
	m := sweepModel(30)
	for i := 0; i < 5; i++ {
		mm, _ := m.Update(cursorKey("j"))
		m = mm.(Model)
	}
	if !m.ctxPending {
		t.Fatal("the sweep should have scheduled a trailing load")
	}

	mm, cmd := m.Update(ctxDebounceMsg{gen: m.ctxGen - 1}) // an earlier row's tick
	m = mm.(Model)
	if cmd != nil {
		t.Fatal("a superseded debounce tick must not load context")
	}
	if !m.ctxPending {
		t.Fatal("a superseded tick must leave the pending load scheduled")
	}

	mm, cmd = m.Update(ctxDebounceMsg{gen: m.ctxGen}) // the current one
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("the current debounce tick must load context")
	}
	if m.ctxPending {
		t.Fatal("the pending flag should clear once the load fires")
	}
}

// Typing a repo filter re-picks the highlighted repo on every keystroke, so the
// pre-debounce code loaded context per character: typing "authoring" cost nine
// `git log --graph --all` walks. It has to settle like a cursor sweep does.
func TestTUI_FilterTypingDebouncesContextLoads(t *testing.T) {
	m := sweepModel(30)
	m.filtering = true
	m.filterPanel = panelRepos

	loads := 0
	for _, r := range "authoring" {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(Model)
		if !m.ctxPending {
			loads++
		}
	}

	if loads > 2 {
		t.Fatalf("typing a 9-character filter loaded context %d times; want at most 2", loads)
	}
}
