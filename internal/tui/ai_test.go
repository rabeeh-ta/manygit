package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rabeeh-ta/manygit/internal/discover"
	"github.com/rabeeh-ta/manygit/internal/git"
	"github.com/rabeeh-ta/manygit/internal/harness"
)

// stubHarness swaps the one seam that reaches a real AI CLI. Every test here goes
// through it, so the suite never shells out to claude or codex.
func stubHarness(t *testing.T, reply string, err error) *string {
	t.Helper()
	var gotPrompt string
	prev := askHarness
	askHarness = func(_ context.Context, _ harness.Harness, _, prompt string) (string, error) {
		gotPrompt = prompt
		return reply, err
	}
	t.Cleanup(func() { askHarness = prev })
	return &gotPrompt
}

// fakeClaudeOnPath puts a stand-in claude on PATH. `:` only opens when
// harness.Available finds the binary, and CI has no claude; askHarness is
// stubbed, so the stand-in never runs.
func fakeClaudeOnPath(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func aiModel(t *testing.T) Model {
	t.Helper()
	fakeClaudeOnPath(t)
	cfg, repos := twoRepos(t)
	cfg.Harness = "claude"
	return loadAll(t, New(cfg, "", repos, nil), 120, 40)
}

func key(m Model, s string) Model {
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return mm.(Model)
}

func typeIn(m Model, s string) Model {
	for _, r := range s {
		t := tea.KeyRunes
		if r == ' ' {
			t = tea.KeySpace // bubbletea re-types a lone space; the handler must cope
		}
		mm, _ := m.Update(tea.KeyMsg{Type: t, Runes: []rune{r}})
		m = mm.(Model)
	}
	return m
}

// A natural-language prompt is mostly spaces. bubbletea delivers a lone space as
// KeySpace, not KeyRunes — the bug that makes `/` unable to hold one. If this
// regresses, every request silently becomes one long unpunctuated word.
func TestAI_PromptAcceptsSpaces(t *testing.T) {
	m := key(aiModel(t), ":")
	if !m.aiPrompting {
		t.Fatal(": should open the prompt")
	}
	m = typeIn(m, "rebase current onto master")
	if m.aiPrompt != "rebase current onto master" {
		t.Fatalf("aiPrompt = %q — spaces were dropped", m.aiPrompt)
	}
}

func TestAI_PromptShowsHarnessAndCancels(t *testing.T) {
	m := key(aiModel(t), ":")
	m = typeIn(m, "merge main")
	if v := stripANSI(m.View()); !strings.Contains(v, "claude:") {
		t.Errorf("the prompt must be named after the harness; view:\n%s", v)
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.aiPrompting || m.aiPrompt != "" {
		t.Error("esc should cancel the prompt and clear it")
	}
}

// Backspace must delete a character, not a byte.
func TestAI_BackspaceIsRuneSafe(t *testing.T) {
	m := key(aiModel(t), ":")
	m = typeIn(m, "tag é")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = mm.(Model)
	if m.aiPrompt != "tag " {
		t.Errorf("aiPrompt = %q, want %q — a rune was half-deleted", m.aiPrompt, "tag ")
	}
}

// While typing, keys that mean something elsewhere must land in the sentence
// instead of firing. Otherwise "get graph" opens the graph overlay mid-word.
func TestAI_PromptSwallowsOtherKeys(t *testing.T) {
	m := key(aiModel(t), ":")
	m = typeIn(m, "g")
	if m.showGraph {
		t.Error("g while typing must not open the graph")
	}
	m = typeIn(m, "?")
	if m.showHelp {
		t.Error("? while typing must not open the help overlay")
	}
	if m.aiPrompt != "g?" {
		t.Errorf("aiPrompt = %q, want %q", m.aiPrompt, "g?")
	}
}

func TestAI_TabCompletesRepoName(t *testing.T) {
	m := key(aiModel(t), ":")
	m = typeIn(m, "rebase al") // fixture repos are "alpha" and "bravo"
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = mm.(Model)
	if m.aiPrompt != "rebase alpha" {
		t.Errorf("tab gave %q, want %q", m.aiPrompt, "rebase alpha")
	}
}

// The whole safety model: a plan is shown and waits. Nothing runs on enter.
func TestAI_PlanWaitsForConfirm(t *testing.T) {
	stubHarness(t, `{"steps":[{"repo":"alpha","args":["fetch"]}],"note":""}`, nil)
	m := key(aiModel(t), ":")
	m = typeIn(m, "fetch alpha")
	m = pumpEnter(t, m)

	if !m.confirmPlan {
		t.Fatal("a valid plan must arm the confirm, not run")
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "git fetch") {
		t.Errorf("the exact command must be shown before it runs; view:\n%s", v)
	}
	if !strings.Contains(v, "[y/N]") {
		t.Error("the confirm prompt must be visible")
	}
	// Anything but y abandons it.
	m = key(m, "n")
	if m.confirmPlan {
		t.Error("n should drop the plan")
	}
	if !strings.Contains(stripANSI(m.View()), "nothing ran") {
		t.Error("declining should say plainly that nothing ran")
	}
}

// A refused plan must never reach the confirm — the user should not be one
// keystroke from running something the validator already rejected.
func TestAI_RefusedPlanIsNeverOffered(t *testing.T) {
	stubHarness(t, `{"steps":[{"repo":"alpha","args":["push","--force"]}],"note":""}`, nil)
	m := key(aiModel(t), ":")
	m = typeIn(m, "force push alpha")
	m = pumpEnter(t, m)

	if m.confirmPlan {
		t.Fatal("a refused plan must not arm the confirm")
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "refused") {
		t.Errorf("the refusal must be shown; view:\n%s", v)
	}
	if !strings.Contains(v, "force-pushes") {
		t.Errorf("the reason must be shown; view:\n%s", v)
	}
}

// "mkdir isn't git" is a sentence, not an error.
func TestAI_DeclineShowsTheNote(t *testing.T) {
	stubHarness(t, `{"steps":[],"note":"manygit only runs git, so I can't create that folder."}`, nil)
	m := key(aiModel(t), ":")
	m = typeIn(m, "mkdir docs")
	m = pumpEnter(t, m)

	if m.confirmPlan {
		t.Error("an empty plan must not arm a confirm")
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "only runs git") {
		t.Errorf("the note must be shown; view:\n%s", v)
	}
	// Nothing is carried between requests, so an empty plan ends the exchange.
	// Without this the user is left waiting for a reply that cannot come.
	if !strings.Contains(v, "press : to ask again") {
		t.Errorf("an empty plan must say how to start another; view:\n%s", v)
	}
}

func TestAI_HarnessErrorIsReported(t *testing.T) {
	stubHarness(t, "", errors.New("claude exploded"))
	m := key(aiModel(t), ":")
	m = typeIn(m, "anything")
	m = pumpEnter(t, m)

	if m.confirmPlan {
		t.Error("a failed request must not arm a confirm")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "harness error") {
		t.Errorf("the failure must be visible; view:\n%s", v)
	}
}

// The context is what makes a plan right, so the prompt must actually carry it.
func TestAI_PromptCarriesTheContext(t *testing.T) {
	got := stubHarness(t, `{"steps":[],"note":"ok"}`, nil)
	m := key(aiModel(t), ":")
	m = typeIn(m, "rebase everything")
	pumpEnter(t, m)

	for _, want := range []string{"rebase everything", "alpha", "bravo", "cursor repo:", "Only git"} {
		if !strings.Contains(*got, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}

// @script.sh must put the file's CONTENTS in the prompt — without them the
// harness cannot know which part of the script the request is asking for.
func TestAI_AtReferenceSendsFileContents(t *testing.T) {
	fakeClaudeOnPath(t)
	got := stubHarness(t, `{"steps":[],"note":"ok"}`, nil)
	cfg, repos := twoRepos(t)
	cfg.Harness = "claude"
	root := filepath.Dir(repos[0].Path)
	body := "#!/bin/sh\n# frontend\ngit -C web pull --ff-only\n# backend\ngit -C api pull --ff-only\n"
	if err := os.WriteFile(filepath.Join(root, "update-all.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	scripts := discover.Scripts(root, 2, nil)
	m := loadAll(t, New(cfg, root, repos, scripts), 120, 40)

	m = key(m, ":")
	m = typeIn(m, "@update-all.sh only the frontend part")
	pumpEnter(t, m)

	if !strings.Contains(*got, "git -C web pull") {
		t.Errorf("the referenced script's contents must reach the harness:\n%s", *got)
	}
	if !strings.Contains(*got, "update-all.sh") {
		t.Error("the reference name should be in the prompt")
	}
}

// Tab completes a script reference, so you do not have to remember its path.
func TestAI_TabCompletesScriptReference(t *testing.T) {
	fakeClaudeOnPath(t)
	cfg, repos := twoRepos(t)
	cfg.Harness = "claude"
	root := filepath.Dir(repos[0].Path)
	if err := os.WriteFile(filepath.Join(root, "update-all.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := loadAll(t, New(cfg, root, repos, discover.Scripts(root, 2, nil)), 120, 40)
	m = key(m, ":")
	m = typeIn(m, "@upd")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := mm.(Model).aiPrompt; got != "@update-all.sh" {
		t.Errorf("tab gave %q, want %q", got, "@update-all.sh")
	}
}

// The completion is debounced: while keys are still landing it stays hidden, so
// the line doesn't visibly churn as the suggestion grows, shrinks and vanishes.
// Costing is not the reason — it is the same work at 40 names and 2000.
func TestAI_GhostIsHiddenWhileTyping(t *testing.T) {
	m := key(aiModel(t), ":")
	m = typeIn(m, "rebase al") // "alpha" would match immediately

	if m.aiGhost {
		t.Error("the completion must stay hidden while typing")
	}
	if v := stripANSI(m.View()); strings.Contains(v, "rebase alpha") {
		t.Errorf("the ghost was rendered mid-burst; view:\n%s", v)
	}

	// The tick belonging to the last keystroke reveals it.
	mm, _ := m.Update(aiGhostMsg{gen: m.aiGhostGen})
	m = mm.(Model)
	if !m.aiGhost {
		t.Fatal("after the settle tick the completion should show")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "rebase alpha") {
		t.Errorf("the ghost should now be visible; view:\n%s", v)
	}
}

// A tick from an earlier keystroke must not reveal a stale suggestion — that is
// exactly the flicker the debounce exists to remove.
func TestAI_StaleGhostTickIsIgnored(t *testing.T) {
	m := key(aiModel(t), ":")
	m = typeIn(m, "rebase a")
	stale := m.aiGhostGen
	m = typeIn(m, "l") // a newer key supersedes it

	mm, _ := m.Update(aiGhostMsg{gen: stale})
	if mm.(Model).aiGhost {
		t.Error("a superseded tick must not reveal the completion")
	}
}

// tab is explicit, so it completes whether or not the ghost happens to be shown.
func TestAI_TabCompletesEvenWhileGhostHidden(t *testing.T) {
	m := key(aiModel(t), ":")
	m = typeIn(m, "rebase al")
	if m.aiGhost {
		t.Fatal("precondition: the ghost should be hidden mid-typing")
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := mm.(Model).aiPrompt; got != "rebase alpha" {
		t.Errorf("tab gave %q, want %q", got, "rebase alpha")
	}
}

// A long note must WRAP in the Output pane, not run off the edge. It is wrapped
// by the renderer, which is the only place that knows the pane's real width — so
// this also covers the case that was broken before: a note wrapped against a
// guessed width and then truncated, losing the end of the sentence.
func TestAI_LongNoteWrapsInsteadOfBeingCut(t *testing.T) {
	long := "I can't commit for you because your standing rule is that you manage all git " +
		"state yourself, and four dirty paths would need staging decisions you have not made yet."
	stubHarness(t, `{"steps":[],"note":`+quote(long)+`}`, nil)
	m := key(aiModel(t), ":")
	m = typeIn(m, "commit everything")
	m = pumpEnter(t, m)

	v := stripANSI(m.View())
	// The tail of the sentence has to be on screen. Assert on a single word: the
	// note is wrapped, so any phrase may legitimately straddle a line break.
	if !strings.Contains(v, "decisions") {
		t.Errorf("the end of the note was cut off; view:\n%s", v)
	}
	// And nothing may exceed the terminal width.
	for _, ln := range strings.Split(v, "\n") {
		if w := lipgloss.Width(ln); w > 120 {
			t.Errorf("line is %d cells wide, terminal is 120: %q", w, ln)
		}
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Up/down walks the prompts you've already sent this session. It is in-memory
// only — a fresh launch starts empty, by design.
func TestAI_HistoryUpDown(t *testing.T) {
	stubHarness(t, `{"steps":[],"note":"ok"}`, nil)
	m := aiModel(t)
	for _, req := range []string{"fetch alpha", "rebase bravo"} {
		m = key(m, ":")
		m = typeIn(m, req)
		m = pumpEnter(t, m)
	}

	up := func(m Model) Model { mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp}); return mm.(Model) }
	down := func(m Model) Model { mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown}); return mm.(Model) }

	m = key(m, ":")
	m = typeIn(m, "half-typed")
	m = up(m)
	if m.aiPrompt != "rebase bravo" {
		t.Errorf("first up = %q, want the most recent request", m.aiPrompt)
	}
	m = up(m)
	if m.aiPrompt != "fetch alpha" {
		t.Errorf("second up = %q, want the older request", m.aiPrompt)
	}
	m = up(m) // clamps at the oldest
	if m.aiPrompt != "fetch alpha" {
		t.Errorf("up past the oldest = %q, should clamp", m.aiPrompt)
	}
	m = down(m)
	m = down(m)
	if m.aiPrompt != "half-typed" {
		t.Errorf("down past the newest = %q — the draft must come back", m.aiPrompt)
	}
}

func TestAI_HistorySkipsConsecutiveDuplicates(t *testing.T) {
	stubHarness(t, `{"steps":[],"note":"ok"}`, nil)
	m := aiModel(t)
	for i := 0; i < 3; i++ {
		m = key(m, ":")
		m = typeIn(m, "same request")
		m = pumpEnter(t, m)
	}
	if len(m.aiHistory) != 1 {
		t.Errorf("history = %v, want one entry", m.aiHistory)
	}
}

// A long sentence needs word- and line-deletes; plain backspace alone is painful.
func TestAI_WordAndLineDelete(t *testing.T) {
	m := key(aiModel(t), ":")
	m = typeIn(m, "rebase alpha onto master")

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}) // opt+backspace
	m = mm.(Model)
	if m.aiPrompt != "rebase alpha onto " {
		t.Errorf("alt+backspace = %q, want the last word gone", m.aiPrompt)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlW}) // same thing, terminals without alt
	m = mm.(Model)
	if m.aiPrompt != "rebase alpha " {
		t.Errorf("ctrl+w = %q, want another word gone", m.aiPrompt)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU}) // clear the line
	if got := mm.(Model).aiPrompt; got != "" {
		t.Errorf("ctrl+u = %q, want the line cleared", got)
	}
}

// `:` with no usable harness must explain itself rather than opening a dead input.
func TestAI_NoHarnessExplainsItself(t *testing.T) {
	cfg, repos := twoRepos(t)
	cfg.Harness = "definitely-not-installed"
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m = key(m, ":")
	if m.aiPrompting {
		t.Fatal(": must not open a prompt with no harness")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "no AI harness") {
		t.Errorf("it should say why; view:\n%s", v)
	}
}

// y runs the plan for real, against the fixture's actual git repositories, and
// the Output pane reports each step. This is the only test that proves the whole
// chain — prompt, harness, validate, confirm, exec — actually moves git.
func TestAI_ConfirmRunsThePlanForReal(t *testing.T) {
	fakeClaudeOnPath(t)
	stubHarness(t, `{"steps":[{"repo":"alpha","args":["tag","v0.0.9"]}],"note":""}`, nil)
	cfg, repos := twoRepos(t)
	cfg.Harness = "claude"
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	alpha := ""
	for _, r := range repos {
		if r.Name == "alpha" {
			alpha = r.Path
		}
	}

	m = key(m, ":")
	m = typeIn(m, "tag alpha")
	m = pumpEnter(t, m)
	if !m.confirmPlan {
		t.Fatal("expected a confirm")
	}

	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("y should have produced the exec command")
	}
	// y now returns a tea.Batch — the exec plus the repo probe that follows the
	// commands live — so the exec message has to be pulled out of the batch
	// rather than assumed to be the single return. fastMsgs drops the probe's
	// 1.2s Tick, which is exactly what a real event loop would still be waiting on.
	for _, msg := range fastMsgs(t, cmd, 2*time.Second) {
		mm, _ = m.Update(msg)
		m = mm.(Model)
	}

	if out, err := gitOut(alpha, "tag", "--list"); err != nil || out != "v0.0.9" {
		t.Fatalf("tag list = %q (err %v) — the plan did not actually run", out, err)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "git tag v0.0.9") || !strings.Contains(v, "ok") {
		t.Errorf("Output should report the step; view:\n%s", v)
	}
	if m.outputRunning {
		t.Error("outputRunning should be cleared when the plan finishes")
	}
}

func gitOut(dir string, args ...string) (string, error) {
	return git.Run(dir, args...)
}

// pumpEnter submits the prompt and delivers the resulting command's message,
// the way the bubbletea runtime would.
func pumpEnter(t *testing.T, m Model) Model {
	t.Helper()
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("enter should have produced a harness command")
	}
	mm, _ = m.Update(cmd())
	return mm.(Model)
}
