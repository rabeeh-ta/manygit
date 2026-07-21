package tui

import (
	"bufio"

	"manygit/internal/discover"
	"manygit/internal/gh"
	"manygit/internal/git"
	"manygit/internal/selfupdate"
)

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
	path    string
	skipped bool
	reason  string
	err     error
}

type discardDoneMsg struct {
	path string
	full bool // D (also removed untracked) vs d (tracked changes only)
	err  error
}

// changelogMsg carries the releases fetched for the post-update changelog. from
// is the version we updated from (for the "you were here" marker). On err, or an
// empty list, the screen is simply not shown — the app never blocks on it.
type changelogMsg struct {
	from     string
	releases []selfupdate.Release
	err      error
}

// rescanMsg carries the result of re-walking the root at a new depth. depth is
// the *candidate* — it is only committed to the config if the walk found repos,
// so the setting can never leave the Repos pane empty (main.go refuses to start
// with zero repos; the `,` settings screen must not be able to reach that state either).
type rescanMsg struct {
	depth int
	repos []discover.Repo
	err   error
}

// openDoneMsg is the result of launching the editor on a repo (o). err is set
// when the command couldn't be started (e.g. not found) or exited non-zero
// quickly (e.g. VS Code's "only available inside a VS Code terminal"); nil when
// it launched cleanly or is still running.
type openDoneMsg struct {
	path string
	err  error
}

type latestTagMsg struct {
	path string
	tag  string
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

// scriptOutMsg carries one live line of a running script's combined stdout+stderr.
// done=true signals EOF; err is then the script's non-zero exit (or a read error),
// nil on clean exit. scanner is the shared reader the next read resumes from.
type scriptOutMsg struct {
	run     int // the run this line belongs to (Model.outputRun at start)
	scanner *bufio.Scanner
	line    string
	done    bool
	err     error
}

// statusExpireMsg clears the status line if gen still matches the latest set.
type statusExpireMsg struct {
	gen int
}

// newsFeedMsg carries AI-summarized commit headlines for the top-bar feed.
type newsFeedMsg struct {
	gen       int
	headlines []string
	err       error
}

// newsTickMsg rotates the top-bar headline (dropped if gen is stale).
type newsTickMsg struct {
	gen int
}

// newsDebounceMsg fires a beat after a fetch; only the latest one refreshes the
// news, coalescing a burst of fetches into one refresh.
type newsDebounceMsg struct {
	gen int
}

// ctxDebounceMsg fires ctxSettle after a repo-cursor move that arrived mid-sweep;
// only the latest one (matching gen) actually loads the highlighted repo's
// context, so holding j collapses into a single load instead of one per row.
type ctxDebounceMsg struct {
	gen int
}

// graphMsg carries the colored graph lines plus commit entries for the graph view.
type graphMsg struct {
	path    string
	lines   []string
	commits []git.GraphEntry
	err     error
}

// ghProbeMsg reports gh readiness, resolved once at startup by ghProbeCmd:
// installed = binary on PATH; available = installed AND authenticated; user is
// the login when available.
type ghProbeMsg struct {
	installed bool
	available bool
	user      string
}

// prsMsg carries one of the two PR lists: review==true is the review-requested
// list, false is the user's own open PRs. err is set when the gh query failed.
type prsMsg struct {
	review bool
	prs    []gh.PullRequest
	err    error
}

// prCheckoutDoneMsg is the result of `gh pr checkout` for a PR: path is the local
// repo it ran in, number identifies the PR.
type prCheckoutDoneMsg struct {
	path   string
	number int
	err    error
}
