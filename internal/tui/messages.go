package tui

import "manygit/internal/git"

type statusMsg struct {
	path string
	st   git.RepoStatus
}

type fetchDoneMsg struct {
	path string
	err  error
}

type syncDoneMsg struct {
	path    string
	skipped bool
	reason  string
	err     error
}

type pushDoneMsg struct {
	path string
	err  error
}

type branchesMsg struct {
	path     string
	branches []git.Branch
	err      error
}

type logMsg struct {
	path  string
	lines []string
	err   error
}

type checkoutDoneMsg struct {
	path   string
	branch string
	err    error
}

type scriptDoneMsg struct {
	name string
	err  error
}
