package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func escapeKeyAt(m Model, key tea.KeyMsg, at time.Time) (Model, bool) {
	next, cmd := m.handleKeyAt(key, at)
	quit := false
	if cmd != nil {
		_, quit = cmd().(tea.QuitMsg)
	}
	return next.(Model), quit
}

func TestDoubleEscapeTiming(t *testing.T) {
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	at := time.Now()
	for _, tc := range []struct {
		name string
		gap  time.Duration
		quit bool
	}{
		{"rapid", 100 * time.Millisecond, true},
		{"boundary", doubleEscapeWindow, true},
		{"expired", doubleEscapeWindow + time.Nanosecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, quit := escapeKeyAt(Model{}, esc, at)
			if quit {
				t.Fatal("first Escape quit")
			}
			m, quit = escapeKeyAt(m, esc, at.Add(tc.gap))
			if quit != tc.quit {
				t.Fatalf("quit = %v, want %v", quit, tc.quit)
			}
			if !quit {
				_, quit = escapeKeyAt(m, esc, at.Add(tc.gap+time.Millisecond))
				if !quit {
					t.Fatal("expired press did not start a fresh pair")
				}
			}
		})
	}
}

func TestDoubleEscapeOtherKeyResets(t *testing.T) {
	at := time.Now()
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	m, _ := escapeKeyAt(Model{}, esc, at)
	m, _ = escapeKeyAt(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}, at.Add(time.Millisecond))
	_, quit := escapeKeyAt(m, esc, at.Add(2*time.Millisecond))
	if quit {
		t.Fatal("Escape after another key quit")
	}
}

func TestDoubleEscapeAcrossLayers(t *testing.T) {
	at := time.Now()
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	for _, tc := range []struct {
		name   string
		model  Model
		closed func(Model) bool
	}{
		{"help", Model{showHelp: true}, func(m Model) bool { return !m.showHelp }},
		{"graph", Model{showGraph: true}, func(m Model) bool { return !m.showGraph }},
		{"news", Model{showNews: true}, func(m Model) bool { return !m.showNews }},
		{"changelog", Model{showChangelog: true}, func(m Model) bool { return !m.showChangelog }},
		{"filter", Model{filtering: true}, func(m Model) bool { return !m.filtering }},
		{"shell", Model{shellPrompting: true}, func(m Model) bool { return !m.shellPrompting }},
		{"ai", Model{aiPrompting: true}, func(m Model) bool { return !m.aiPrompting }},
		{"discard", Model{confirmDiscard: true}, func(m Model) bool { return !m.confirmDiscard }},
		{"plan", Model{confirmPlan: true}, func(m Model) bool { return !m.confirmPlan }},
		{"zoom", Model{zoomed: true}, func(m Model) bool { return !m.zoomed }},
		{"diff", Model{focus: panelBottom, bottomView: bvChanges, changeShowDiff: true}, func(m Model) bool { return !m.changeShowDiff && m.bottomView == bvChanges }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, quit := escapeKeyAt(tc.model, esc, at)
			if quit || !tc.closed(m) {
				t.Fatal("first Escape did not back out normally")
			}
			// Background UI messages must not interrupt a consecutive key sequence.
			next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			_, quit = escapeKeyAt(next.(Model), esc, at.Add(100*time.Millisecond))
			if !quit {
				t.Fatal("second Escape did not quit")
			}
		})
	}
}
