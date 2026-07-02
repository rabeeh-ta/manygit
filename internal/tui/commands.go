package tui

import (
	"bufio"
	"io"
	"os/exec"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/git"
)

// statusCmd loads local status (ungated — fast local read).
func statusCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return statusMsg{path: path, st: git.Status(path)}
	}
}

// gated runs fn while holding a semaphore slot.
func gated(sem chan struct{}, fn func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		sem <- struct{}{}
		defer func() { <-sem }()
		return fn()
	}
}

func fetchCmd(sem chan struct{}, path string) tea.Cmd {
	return gated(sem, func() tea.Msg { return fetchDoneMsg{path: path, err: git.Fetch(path)} })
}

func syncCmd(sem chan struct{}, path string) tea.Cmd {
	return gated(sem, func() tea.Msg { return syncDoneMsg{path: path, err: git.PullFFOnly(path)} })
}

func pushCmd(sem chan struct{}, path string) tea.Cmd {
	return gated(sem, func() tea.Msg { return pushDoneMsg{path: path, err: git.Push(path)} })
}

// discardCmd discards a repo's changes: full=true also deletes untracked files
// (D); false reverts only tracked changes (d). Runs only after user confirmation.
func discardCmd(sem chan struct{}, path string, full bool) tea.Cmd {
	return gated(sem, func() tea.Msg {
		var err error
		if full {
			err = git.DiscardAll(path)
		} else {
			err = git.DiscardTracked(path)
		}
		return discardDoneMsg{path: path, full: full, err: err}
	})
}

func branchesCmd(path string) tea.Cmd {
	return func() tea.Msg {
		b, err := git.Branches(path)
		return branchesMsg{path: path, branches: b, err: err}
	}
}

// graphCmd loads the colored graph plus its commit entries (line index + hash).
func graphCmd(path string, limit int) tea.Cmd {
	return func() tea.Msg {
		lines, commits, err := git.GraphLogEntries(path, limit)
		return graphMsg{path: path, lines: lines, commits: commits, err: err}
	}
}

// changesCmd loads the changed files of the selected graph entry. ref == ""
// means the working tree (WIP); otherwise a commit hash.
func changesCmd(path, ref string) tea.Cmd {
	return func() tea.Msg {
		var files []git.FileChange
		var err error
		if ref == "" {
			files, err = git.StatusFiles(path)
		} else {
			files, err = git.CommitFiles(path, ref)
		}
		return changesMsg{path: path, ref: ref, files: files, err: err}
	}
}

// diffCmd loads the colored diff of one file for the selected graph entry.
func diffCmd(path, ref, file string) tea.Cmd {
	return func() tea.Msg {
		var lines []string
		var err error
		if ref == "" {
			lines, err = git.WorkingFileDiff(path, file)
		} else {
			lines, err = git.CommitFileDiff(path, ref, file)
		}
		return diffMsg{path: path, ref: ref, lines: lines, err: err}
	}
}

// startScriptCmd runs a script with `bash` in the background (non-interactive),
// merging stdout+stderr into one pipe and reading the first line. The process's
// exit status is delivered to the reader via CloseWithError, so a non-zero exit
// surfaces as scanner.Err() at EOF (no shared state, no race).
func startScriptCmd(path string, run int) tea.Cmd {
	return func() tea.Msg {
		c := exec.Command("bash", path)
		c.Dir = filepath.Dir(path)
		pr, pw := io.Pipe()
		c.Stdout, c.Stderr = pw, pw
		if err := c.Start(); err != nil {
			return scriptOutMsg{run: run, done: true, err: err}
		}
		// Deliver the exit status to the reader: a non-zero exit surfaces as
		// scanner.Err() at EOF. If the TUI quits mid-stream this goroutine is
		// abandoned, but the child then gets SIGPIPE on its next write and exits.
		go func() { pw.CloseWithError(c.Wait()) }()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // tolerate long lines (1 MiB)
		return readScriptLine(sc, run)()
	}
}

// readScriptLine reads the next line from a running script, re-issued after each
// scriptOutMsg to drive the stream until EOF.
func readScriptLine(sc *bufio.Scanner, run int) tea.Cmd {
	return func() tea.Msg {
		if sc.Scan() {
			return scriptOutMsg{run: run, scanner: sc, line: sc.Text()}
		}
		return scriptOutMsg{run: run, scanner: sc, done: true, err: sc.Err()}
	}
}

func checkoutCmd(sem chan struct{}, path, branch string) tea.Cmd {
	return gated(sem, func() tea.Msg {
		return checkoutDoneMsg{path: path, branch: branch, err: git.Checkout(path, branch)}
	})
}
