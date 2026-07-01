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

// changesMsg carries the changed files of the selected graph entry (ref==""=WIP).
type changesMsg struct {
	path  string
	ref   string
	files []git.FileChange
	err   error
}

// diffMsg carries the colored diff lines of one selected file. path/ref identify
// the repo + graph entry it was loaded for, so a stale async diff is dropped.
type diffMsg struct {
	path  string
	ref   string
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

// statusExpireMsg clears the status line if gen still matches the latest set.
type statusExpireMsg struct {
	gen int
}

// graphMsg carries the colored graph lines plus commit entries for the graph view.
type graphMsg struct {
	path    string
	lines   []string
	commits []git.GraphEntry
	err     error
}
