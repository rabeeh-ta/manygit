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

func logCmd(path string, limit int) tea.Cmd {
	return func() tea.Msg {
		lines, err := git.GraphLog(path, limit)
		return logMsg{path: path, lines: lines, err: err}
	}
}

// graphCmd loads a larger colored graph for the full-screen graph view.
func graphCmd(path string, limit int) tea.Cmd {
	return func() tea.Msg {
		lines, err := git.GraphLog(path, limit)
		return graphMsg{path: path, lines: lines, err: err}
	}
}

func checkoutCmd(sem chan struct{}, path, branch string) tea.Cmd {
	return gated(sem, func() tea.Msg {
		return checkoutDoneMsg{path: path, branch: branch, err: git.Checkout(path, branch)}
	})
}
