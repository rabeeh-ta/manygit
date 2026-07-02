package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/git"
)

// Cancelling with esc while the agent is thinking must reset the phase, so a
// late harness reply can't surface stale, unrequested commands.
func TestTUI_AgentThinkingEscDropsStaleReply(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("7")})
	m = mm.(Model)
	m.agentPhase = agentPhaseThinking // pretend a request is in flight
	// esc (nav mode) cancels the wait and resets the phase
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.agentPhase != agentPhaseInput {
		t.Fatalf("thinking+esc should reset the phase to input, got %d", m.agentPhase)
	}
	// the late reply from the abandoned request must be dropped
	mm, _ = m.Update(agentProposedMsg{commands: []string{"cd /a && git push --force"}})
	m = mm.(Model)
	if m.agentPhase == agentPhaseProposed {
		t.Error("a stale reply after cancelling must NOT surface proposed commands")
	}
}

func TestParseCommands(t *testing.T) {
	got := parseCommands("```sh\ncd /a && git merge x\n\n# risky: no upstream\ncd /b && git pull\n```")
	want := []string{"cd /a && git merge x", "# risky: no upstream", "cd /b && git pull"}
	if len(got) != len(want) {
		t.Fatalf("parseCommands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cmd %d = %q, want %q", i, got[i], want[i])
		}
	}
	if !isNote("# note") || isNote("git status") {
		t.Error("isNote misclassified a line")
	}
}

func TestWorkspaceContext(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := New(cfg, repos, nil)
	m.repos[0].status = git.RepoStatus{Branch: "main", Ahead: 2, HasUpstream: true}
	ctx := m.workspaceContext()
	for _, want := range []string{"alpha", "branch=main", "ahead 2"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("workspace context missing %q:\n%s", want, ctx)
		}
	}
}

// The agent flow in the bottom slot: 7 selects it (nav mode); enter starts
// composing; type an instruction; enter with no harness errors; a proposed msg
// shows commands; n discards; enter runs (→running); executed msg → done; j
// scrolls; a number key still navigates away. Never calls the real harness or
// executes commands.
func TestTUI_AgentFlow(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)

	// 7 selects the agent bottom view, in nav mode (not typing).
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("7")})
	m = mm.(Model)
	if m.focus != panelBottom || m.bottomView != bvAgent || m.agentPhase != agentPhaseInput {
		t.Fatal("7 should select the agent bottom view in the input phase")
	}
	if m.agentTyping {
		t.Fatal("the agent should start in nav mode, not typing")
	}
	// enter starts composing; then type the instruction.
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if !m.agentTyping {
		t.Fatal("enter should start composing an instruction")
	}
	for _, r := range "merge" {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(Model)
	}
	if m.agentInputBuf != "merge" {
		t.Errorf("input buf = %q", m.agentInputBuf)
	}
	// enter with an uninstalled harness → error, stops typing, stays on input (no CLI call)
	m.cfg.Harness = "definitely-not-installed"
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.agentPhase != agentPhaseInput || m.agentErr == "" || m.agentTyping {
		t.Errorf("no harness should error on the input, phase=%d err=%q typing=%v", m.agentPhase, m.agentErr, m.agentTyping)
	}
	// simulate the harness having proposed commands
	m.agentPhase = agentPhaseThinking
	mm, _ = m.Update(agentProposedMsg{commands: []string{"cd /a && git merge x"}})
	m = mm.(Model)
	if m.agentPhase != agentPhaseProposed || len(m.agentCommands) != 1 {
		t.Fatal("a proposed msg should move to the proposed phase")
	}
	// n discards back to input (nav mode)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = mm.(Model)
	if m.agentPhase != agentPhaseInput {
		t.Error("n should discard back to input")
	}
	// re-propose and confirm → running (do NOT execute the returned command)
	m.agentPhase = agentPhaseProposed
	m.agentCommands = []string{"cd /a && git status"}
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.agentPhase != agentPhaseRunning || cmd == nil {
		t.Fatal("enter on the proposal should run")
	}
	mm, _ = m.Update(agentExecutedMsg{output: []string{"$ cd /a && git status", "clean"}})
	m = mm.(Model)
	if m.agentPhase != agentPhaseDone || len(m.agentOutput) != 2 {
		t.Fatal("an executed msg should move to done with the output")
	}
	// j scrolls the output
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = mm.(Model)
	if m.agentOffset != 1 {
		t.Errorf("j should scroll the output, offset=%d", m.agentOffset)
	}
	// a number key still navigates away from the agent (the whole point of the redo)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	m = mm.(Model)
	if m.bottomView != bvGraph {
		t.Error("4 should switch to the Graph view even from the agent pane")
	}
}
