package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/discover"
)

// drainScript runs a start command to completion, mirroring the Update loop:
// each scriptOutMsg drives the next read until done. Returns the captured lines
// and the terminal error (script's non-zero exit or a read error; nil on clean exit).
func drainScript(t *testing.T, first tea.Cmd) ([]string, error) {
	t.Helper()
	if first == nil {
		t.Fatal("nil start command")
	}
	msg := first()
	var lines []string
	for {
		om, ok := msg.(scriptOutMsg)
		if !ok {
			t.Fatalf("expected scriptOutMsg, got %T", msg)
		}
		if om.done {
			return lines, om.err
		}
		lines = append(lines, om.line)
		if om.scanner == nil {
			t.Fatal("non-done scriptOutMsg is missing its scanner")
		}
		msg = readScriptLine(om.scanner, om.run)()
	}
}

// The streaming reader must capture a real script's combined stdout+stderr and
// surface a non-zero exit as the terminal error.
func TestScriptStreamingRealProcess(t *testing.T) {
	dir := t.TempDir()

	ok := filepath.Join(dir, "ok.sh")
	if err := os.WriteFile(ok, []byte("#!/bin/bash\necho line1\necho err1 >&2\necho line3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	lines, derr := drainScript(t, startScriptCmd(ok, 0))
	if derr != nil {
		t.Errorf("clean exit should have a nil error, got %v", derr)
	}
	if len(lines) != 3 {
		t.Fatalf("want 3 captured lines, got %d: %v", len(lines), lines)
	}
	joined := strings.Join(lines, "\n")
	for _, w := range []string{"line1", "err1", "line3"} {
		if !strings.Contains(joined, w) {
			t.Errorf("captured output %v is missing %q", lines, w)
		}
	}

	fail := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(fail, []byte("#!/bin/bash\necho boom\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	flines, ferr := drainScript(t, startScriptCmd(fail, 0))
	if ferr == nil {
		t.Error("a non-zero exit should surface as the terminal error")
	}
	if len(flines) != 1 || flines[0] != "boom" {
		t.Errorf("want [boom] before the failure, got %v", flines)
	}
}

// enter in the Scripts panel starts the run, flips the bottom slot to the Output
// view, and streamed lines append while the view follows the tail; done clears
// the running flag.
func TestTUI_EnterRunsScriptIntoOutput(t *testing.T) {
	cfg, repos := twoRepos(t)
	scripts := []discover.Script{{Path: "/x/a.sh", Name: "a.sh"}}
	m := loadAll(t, New(cfg, "", repos, scripts), 100, 30)
	m.focus = panelScripts

	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd == nil {
		t.Error("enter should return a run command")
	}
	if m.focus != panelBottom || m.bottomView != bvOutput {
		t.Errorf("enter should focus the Output view, got focus=%d view=%d", m.focus, m.bottomView)
	}
	if !m.outputRunning || m.outputTitle != "a.sh" {
		t.Errorf("enter should mark a.sh running, got running=%v title=%q", m.outputRunning, m.outputTitle)
	}
	run := m.outputRun

	for _, ln := range []string{"one", "two", "three"} {
		mm, _ = m.Update(scriptOutMsg{run: run, line: ln})
		m = mm.(Model)
	}
	if len(m.outputLines) != 3 || m.outputOffset != 2 {
		t.Errorf("output should follow the tail: lines=%v off=%d", m.outputLines, m.outputOffset)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "three") {
		t.Error("Output view should render the streamed lines")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "7 Output*") {
		t.Error("bottom tab bar should mark Output with a running marker")
	}

	// a line from a superseded (stale) run must be ignored
	mm, _ = m.Update(scriptOutMsg{run: run - 1, line: "STALE"})
	m = mm.(Model)
	if len(m.outputLines) != 3 {
		t.Errorf("a stale run's line should be dropped, got %v", m.outputLines)
	}

	mm, _ = m.Update(scriptOutMsg{run: run, done: true})
	m = mm.(Model)
	if m.outputRunning {
		t.Error("done should clear outputRunning")
	}

	// a done from a superseded run must NOT clear the current run's state
	m.outputRunning = true
	mm, _ = m.Update(scriptOutMsg{run: run - 1, done: true})
	m = mm.(Model)
	if !m.outputRunning {
		t.Error("a stale run's done should not clear the current run's outputRunning")
	}
}

// appendOutput follows the tail while pinned to the bottom, but leaves the offset
// alone once the user has scrolled up.
func TestTUI_AppendOutputFollow(t *testing.T) {
	var m Model
	m.appendOutput("a") // 0
	m.appendOutput("b") // follows -> 1
	m.appendOutput("c") // follows -> 2
	if m.outputOffset != 2 {
		t.Fatalf("following tail: want offset 2, got %d", m.outputOffset)
	}
	m.outputOffset = 0 // user scrolled to top
	m.appendOutput("d")
	if m.outputOffset != 0 {
		t.Errorf("after scrolling up, appendOutput should not jump to the tail, got %d", m.outputOffset)
	}
}
