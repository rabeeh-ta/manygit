package tui

import (
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

func checkoutCmd(sem chan struct{}, path, branch string) tea.Cmd {
	return gated(sem, func() tea.Msg {
		return checkoutDoneMsg{path: path, branch: branch, err: git.Checkout(path, branch)}
	})
}
