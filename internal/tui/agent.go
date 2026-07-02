package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"manygit/internal/harness"
)

// agentPhase is where the Agent (7) view is in the one-shot flow.
type agentPhase int

const (
	agentPhaseInput    agentPhase = iota // typing an instruction
	agentPhaseThinking                   // waiting on the harness
	agentPhaseProposed                   // showing proposed commands, awaiting confirm
	agentPhaseRunning                    // executing the confirmed commands
	agentPhaseDone                       // showing the output
)

// harnessDir is the working directory the harness runs in — the highlighted
// repo (or the first repo). The proposed commands carry absolute cd's, so this
// mostly affects the harness's own ambient context.
func (m Model) harnessDir() string {
	if r := m.currentVisible(m.visibleRepos()); r != nil {
		return r.repo.Path
	}
	if len(m.repos) > 0 {
		return m.repos[0].repo.Path
	}
	return "."
}

// workspaceContext summarizes every repo (group, name, path, branch, status) —
// the tree the user sees — as context for the AI harness.
func (m Model) workspaceContext() string {
	var b strings.Builder
	b.WriteString("Multi-repo workspace. Repositories:\n")
	for _, r := range m.repos {
		st := r.status
		branch := st.Branch
		if branch == "" {
			branch = "?"
		}
		var flags []string
		if st.Ahead > 0 {
			flags = append(flags, fmt.Sprintf("ahead %d", st.Ahead))
		}
		if st.Behind > 0 {
			flags = append(flags, fmt.Sprintf("behind %d", st.Behind))
		}
		if st.DirtyCount > 0 {
			flags = append(flags, fmt.Sprintf("%d dirty", st.DirtyCount))
		}
		if !st.HasUpstream {
			flags = append(flags, "no upstream")
		}
		status := "clean"
		if len(flags) > 0 {
			status = strings.Join(flags, ", ")
		}
		group := r.repo.Group
		if group == "" {
			group = "."
		}
		fmt.Fprintf(&b, "- %s/%s  path=%s  branch=%s  (%s)\n", group, r.repo.Name, r.repo.Path, branch, status)
	}
	return b.String()
}

// agentPrompt is the one-shot prompt sent to the harness.
func (m Model) agentPrompt(instruction string) string {
	return fmt.Sprintf(`You generate shell commands (mostly git) for a multi-repo workspace.

%s
Task: %s

Output ONLY the shell command(s) to accomplish the task — one per line, each an
absolute-path cd followed by the command, for example:
cd /abs/path/to/repo && git merge origin/main

Rules: no explanation, no markdown, no backticks, no comments. If the task is
unclear or unsafe, output a single line starting with "# " explaining why.`,
		m.workspaceContext(), strings.TrimSpace(instruction))
}

// agentRunCmd asks the harness to generate commands for the instruction.
func agentRunCmd(h harness.Harness, dir, prompt string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		out, err := h.OneShot(ctx, dir, prompt)
		return agentProposedMsg{commands: parseCommands(out), raw: out, err: err}
	}
}

// parseCommands extracts command lines from harness output, dropping blanks and
// markdown fences. A leading "# " line is kept as a note (shown, not executed).
func parseCommands(out string) []string {
	var cmds []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "```") {
			continue
		}
		cmds = append(cmds, ln)
	}
	return cmds
}

// isNote reports whether a proposed line is an explanatory note, not a command.
func isNote(line string) bool { return strings.HasPrefix(line, "# ") }

// harnessLabel describes the active harness for the agent title.
func (m Model) harnessLabel() string {
	switch {
	case m.cfg.Harness == "":
		return "no AI harness — pick one in ? settings"
	case harness.Available(m.cfg.Harness):
		return "harness: " + m.cfg.Harness
	default:
		return "harness: " + m.cfg.Harness + " (not installed)"
	}
}

// agentBody renders the Agent view inside the bottom slot (view 7): an
// instruction prompt, the harness's proposed commands (reviewed before running),
// and their output. It's small — z zooms the pane for room.
func (m Model) agentBody(contentW, innerH int) string {
	var b []string
	switch m.agentPhase {
	case agentPhaseInput:
		b = append(b, styleDim.Render("instruction (a git action across your repos):"))
		b = append(b, "> "+m.agentInputBuf+"_")
		if m.agentErr != "" {
			b = append(b, styleRed.Render(m.agentErr))
		}
		hint := "enter: ask " + m.cfg.Harness + "    esc: back    z: zoom"
		if !harness.Available(m.cfg.Harness) {
			hint = m.harnessLabel()
		}
		b = append(b, styleDim.Render(hint))
	case agentPhaseThinking:
		b = append(b, styleDim.Render("asking "+m.cfg.Harness+" ..."))
	case agentPhaseProposed:
		b = append(b, styleDim.Render("proposed commands (review before running):"))
		for _, c := range m.agentCommands {
			if isNote(c) {
				b = append(b, styleYellow.Render(c))
			} else {
				b = append(b, styleGreen.Render(c))
			}
		}
		b = append(b, styleDim.Render("enter/y: run    esc/n: discard"))
	case agentPhaseRunning:
		b = append(b, styleDim.Render("running ..."))
	case agentPhaseDone:
		b = append(b, styleDim.Render("output — j/k scroll · enter: new · esc: back"))
		avail := innerH - 1
		if avail < 1 {
			avail = 1
		}
		start, end := window(len(m.agentOutput), m.agentOffset, avail)
		b = append(b, m.agentOutput[start:end]...)
	}
	return lipgloss.NewStyle().MaxWidth(contentW).Render(strings.Join(b, "\n"))
}

// agentExecCmd runs the confirmed commands in sequence, capturing combined
// output. Notes ("# …") are skipped. Runs only after the user confirms.
func agentExecCmd(commands []string) tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		for _, c := range commands {
			if isNote(c) {
				continue
			}
			fmt.Fprintf(&b, "$ %s\n", c)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			cmd := exec.CommandContext(ctx, "bash", "-c", c)
			// Non-interactive: no stdin, and don't let git block on a credential
			// or terminal prompt (would hang the whole run otherwise).
			cmd.Stdin = nil
			cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
			out, err := cmd.CombinedOutput()
			cancel()
			b.Write(out)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				b.WriteByte('\n')
			}
			if err != nil {
				fmt.Fprintf(&b, "[exit: %v]\n", err)
			}
			b.WriteString("\n")
		}
		return agentExecutedMsg{output: strings.Split(strings.TrimRight(b.String(), "\n"), "\n")}
	}
}
