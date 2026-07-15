package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/config"
	"manygit/internal/discover"
	"manygit/internal/gh"
	"manygit/internal/git"
)

type panel int

const (
	panelRepos    panel = iota // key 1
	panelScripts               // key 2
	panelBranches              // keys 3/4 (top-right multi-view: Branches / PRs)
	panelBottom                // keys 5/6/7 (bottom multi-view: graph / changes / output)
	panelCount                 // number of focusable panels

	// filterPRs is a filter-scope marker for the PR sub-view of the Branches
	// panel. It is NOT a focusable panel (never assigned to m.focus, never in tab
	// cycling — it sorts after panelCount); it only tags filterPanel so the PR
	// filter is kept distinct from the branch filter that shares the same panel.
	filterPRs
)

// topView is which view the top-right multi-view slot shows: the highlighted
// repo's branches, or the GitHub PR list.
type topView int

const (
	tvBranches topView = iota // key 3
	tvPRs                     // key 4
)

// bottomView is which view the multi-view bottom-right slot shows.
type bottomView int

const (
	bvGraph   bottomView = iota // key 5
	bvChanges                   // key 6
	bvOutput                    // key 7
)

// repoVM is the per-repo view model.
type repoVM struct {
	repo      discover.Repo
	status    git.RepoStatus
	loaded    bool
	fetching  bool
	latestTag string // most recent tag, shown inline when showTagsInline is on
}

// Model is the Bubble Tea model.
type Model struct {
	cfg   config.Config
	repos []*repoVM

	cursor int
	focus  panel

	filter          string
	filtering       bool
	filterPanel     panel // which list `/` filters: the panel focused when it was pressed
	filterAttention bool  // show only repos with changes / ahead / behind
	showHelp        bool  // the settings + help overlay
	showGraph       bool  // full-screen commit graph overlay
	showNews        bool  // full-screen news-feed overlay (n)
	showTagsInline  bool  // show each repo's latest tag inline in the Repos rows (t)
	zoomed          bool  // maximize the focused pane to full screen (z)

	// settings overlay (?): a cursor over a flat radio-list of choices (each theme,
	// each glyph option, then the editor row); showKeys flips to the keybindings
	// reference; editingOpenCmd/openCmdBuf drive the inline editor edit.
	settingsCursor int
	showKeys       bool
	editingOpenCmd bool
	openCmdBuf     string

	// top-right multi-view slot (Branches / PRs) and bottom multi-view slot
	topView    topView
	bottomView bottomView

	// graph view (4): colored git log --graph with a selectable commit cursor.
	// selectable entries are [WIP, commits...]; graphSel 0 == WIP.
	graphLines   []string
	graphCommits []git.GraphEntry
	graphSel     int
	graphOffset  int // scroll offset for the full-screen `g` overlay

	// changes view (5): files of the selected graph entry, with an in-place diff.
	changeFiles    []git.FileChange
	changeCursor   int
	changeShowDiff bool
	changeDiff     []string
	changeDiffOff  int

	// output view (6): live stdout+stderr of the last script run.
	outputLines   []string
	outputTitle   string
	outputOffset  int
	outputRunning bool
	outputRun     int // bumped per run; stale msgs from a superseded run are dropped

	branches     []git.Branch
	branchCursor int
	scripts      []discover.Script
	scriptCursor int
	statusLine   string
	statusGen    int // bumped on each status set; guards the expiry timer

	// top-bar AI news feed: headlines summarizing recent commit activity,
	// refreshed a beat after a fetch burst settles, rotated by a ticker.
	newsFeed     []string
	newsIndex    int
	newsOffset   int // scroll offset for the full-screen news overlay (n)
	newsGen      int // bumped per refresh; guards stale refreshes/ticks
	newsDebounce int // bumped on each fetch; the latest debounce tick refreshes
	newsLoading  bool

	// PRs view (key 4, in the top-right slot beside Branches): GitHub pull requests
	// via the gh CLI. Two lists — mine and review-requested — toggled by `m`;
	// prCursor indexes the visible (filtered) list. Loaded async after gh is
	// probed; shows a hint when gh is absent.
	prMine       []gh.PullRequest
	prReview     []gh.PullRequest
	prShowReview bool // false = my open PRs, true = review requested of me
	prCursor     int
	prLoaded     bool  // both lists have returned at least once
	prErr        error // last load error (e.g. gh too old for `search prs`)

	// gh availability, resolved once at startup by ghProbeCmd. ghProbed flips true
	// when the probe returns; ghInstalled = binary on PATH; ghAvailable = installed
	// AND authenticated (gates the PR features); ghUser drives the bottom-bar
	// "github: <user>" indicator.
	ghProbed    bool
	ghInstalled bool
	ghAvailable bool
	ghUser      string

	sem           chan struct{}
	width, height int

	// lastFetch is when the most recent fetch burst started. A terminal-focus
	// refetch is skipped if it fired within focusRefetchCooldown of this, so
	// rapid alt-tabbing can't spray git fetches at every remote.
	lastFetch time.Time

	// discard confirm (d / D): armed on a repo; the next key confirms (y) or
	// cancels. full distinguishes D (also delete untracked files) from d (tracked
	// changes only). The path/name are remembered so the confirm hits that repo.
	confirmDiscard     bool
	confirmDiscardFull bool
	confirmDiscardPath string
	confirmDiscardName string
}

// visibleScripts is the scripts list after the `/` filter (when it targets the
// Scripts panel). The scriptCursor, run, and render all index this slice.
func (m Model) visibleScripts() []discover.Script {
	if m.filterPanel != panelScripts || m.filter == "" {
		return m.scripts
	}
	needle := strings.ToLower(m.filter)
	var out []discover.Script
	for _, s := range m.scripts {
		if strings.Contains(strings.ToLower(s.Name), needle) {
			out = append(out, s)
		}
	}
	return out
}

// visibleBranches is the branch list after the `/` filter (when it targets the
// Branches panel). The branchCursor, checkout, and render all index this slice.
// The needle matches the name as shown, so "origin/" narrows to remote branches
// — the practical way to reach one of a repo's hundreds of remote refs.
func (m Model) visibleBranches() []git.Branch {
	if m.filterPanel != panelBranches || m.filter == "" {
		return m.branches
	}
	needle := strings.ToLower(m.filter)
	var out []git.Branch
	for _, b := range m.branches {
		if strings.Contains(strings.ToLower(b.Name), needle) {
			out = append(out, b)
		}
	}
	return out
}

// activePRs returns the PR list the pane currently shows: review-requested when
// prShowReview, otherwise the user's own open PRs.
func (m Model) activePRs() []gh.PullRequest {
	if m.prShowReview {
		return m.prReview
	}
	return m.prMine
}

// visiblePRs is the active PR list after the `/` filter (when it targets the PR
// sub-view, i.e. filterPanel == filterPRs). prCursor and the render index this
// slice. The needle matches the repo slug, title, and author together, so
// "authoring", part of a title, or an author login all narrow the list.
func (m Model) visiblePRs() []gh.PullRequest {
	prs := m.activePRs()
	if m.filterPanel != filterPRs || m.filter == "" {
		return prs
	}
	needle := strings.ToLower(m.filter)
	var out []gh.PullRequest
	for _, p := range prs {
		hay := strings.ToLower(p.RepoSlug + " " + p.Title + " " + p.Author)
		if strings.Contains(hay, needle) {
			out = append(out, p)
		}
	}
	return out
}

// New builds a Model from discovered repos and scripts.
func New(cfg config.Config, repos []discover.Repo, scripts []discover.Script) Model {
	vms := make([]*repoVM, len(repos))
	for i, r := range repos {
		vms[i] = &repoVM{repo: r}
	}
	conc := cfg.Concurrency
	if conc < 1 {
		conc = 1
	}
	applyTheme(themeByName(cfg.Theme)) // set the themeable styles from config
	return Model{
		cfg:     cfg,
		repos:   vms,
		scripts: scripts,
		focus:   panelRepos,
		sem:     make(chan struct{}, conc),
		// topView/bottomView default to their zero values (tvBranches / bvGraph):
		// the top-right shows Branches and the bottom shows Graph on launch.
	}
}

// Init loads local status for every repo (fast, ungated), then fires a
// background fetch (gated by m.sem) for each repo so rows update live.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range m.repos {
		cmds = append(cmds, statusCmd(r.repo.Path))
	}
	for _, r := range m.repos {
		r.fetching = true
		cmds = append(cmds, fetchCmd(m.sem, r.repo.Path))
	}
	if c := m.loadContextCmd(); c != nil {
		cmds = append(cmds, c)
	}
	cmds = append(cmds, ghProbeCmd()) // resolve gh availability, then load PRs
	return tea.Batch(cmds...)
}
