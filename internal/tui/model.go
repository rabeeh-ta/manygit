package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rabeeh-ta/manygit/internal/aigit"
	"github.com/rabeeh-ta/manygit/internal/config"
	"github.com/rabeeh-ta/manygit/internal/discover"
	"github.com/rabeeh-ta/manygit/internal/gh"
	"github.com/rabeeh-ta/manygit/internal/git"
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
	tvBranches   topView = iota // key 3
	tvPRs                       // key 4
	topViewCount                // number of tabs in the top slot; `[` / `]` wrap on it
)

// bottomView is which view the multi-view bottom-right slot shows.
type bottomView int

const (
	bvGraph         bottomView = iota // key 5
	bvChanges                         // key 6
	bvOutput                          // key 7
	bottomViewCount                   // number of tabs in the bottom slot; `[` / `]` wrap on it
)

// shellLocation is where the `!` prompt says you are: the repo's path relative
// to the scanned root. Group is "(root)" for a repo sitting directly in the
// root, which is a label for the Repos pane rather than a path segment — a
// prompt reading "$manygit:(root)/dotfiles" would be nonsense, so drop it.
func shellLocation(r discover.Repo) string {
	if r.Group == "" || r.Group == "(root)" {
		return r.Name
	}
	return r.Group + "/" + r.Name
}

// outputKind is which producer currently owns the Output pane. The pane is
// shared by three of them and they report completion differently: a script
// "ran", a `!` line "exited N", the AI harness prints its own report.
type outputKind int

const (
	outScript outputKind = iota
	outShell
	outAI
)

// repoVM is the per-repo view model.
type repoVM struct {
	repo      discover.Repo
	status    git.RepoStatus
	loaded    bool
	fetching  bool
	latestTag string // most recent tag, shown inline when showTagsInline is on
	// fp is git.Fingerprint at the last probe, or 0 when never sampled. While a
	// script runs it is compared against a fresh sample to find the repos worth
	// re-stat'ing; see repoProbeMsg.
	fp int64
}

// Model is the Bubble Tea model.
type Model struct {
	cfg config.Config
	// root is the directory the repos were discovered under. Kept so the scan
	// can be re-run when the depth setting changes; main.go resolves it once.
	root  string
	repos []*repoVM

	lastEscape time.Time // consecutive Escape presses within doubleEscapeWindow quit

	cursor int
	focus  panel

	filter          string
	filtering       bool
	filterPanel     panel // which list `/` filters: the panel focused when it was pressed
	filterAttention bool  // show only repos with changes / ahead / behind
	showHelp        bool  // the settings + help overlay
	showGraph       bool  // full-screen commit graph overlay
	showNews        bool  // full-screen news-feed overlay (n)
	showChangelog   bool  // post-update changelog overlay (shown once after a self-update)
	showTagsInline  bool  // show each repo's latest tag inline in the Repos rows (t)
	zoomed          bool  // maximize the focused pane to full screen (z)

	// settings overlay (?): a cursor over a flat radio-list of choices (each theme,
	// each glyph option, then the editor row); showKeys flips to the keybindings
	// reference; editingOpenCmd/openCmdBuf drive the inline editor edit.
	settingsCursor int
	showKeys       bool
	keysOffset     int // scroll offset for the keys face (j/k) — it outgrows short terminals
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
	outputRun     int        // bumped per run; stale msgs from a superseded run are dropped
	outputKind    outputKind // which producer owns the pane; words the end-of-run report

	// `!` shell mode. shellCmd is the line being typed; shellLoc/shellDir are
	// the repo it will run in, SNAPSHOTTED when `!` opened — the same reasoning
	// as aiNames: a background fetch or a rescan must not move the target out
	// from under a half-typed command. shellLoc is the repo's path relative to
	// the scanned root ("apps/api-gateway"), which is what the prompt shows.
	//
	// shellCancel is the running command's kill switch (nil when nothing runs),
	// adopted from runStartMsg. shellKilled distinguishes "we stopped it" from a
	// real non-zero exit, so a cancel isn't reported as a crash.
	shellPrompting bool
	shellCmd       string
	shellLoc       string
	shellDir       string
	shellCancel    context.CancelFunc
	shellKilled    bool

	// Prompt history, up/down — in-memory only, exactly like aiHistory: a scratch
	// convenience for this session, and shell lines are not worth persisting to
	// disk by surprise. shellHistIdx == len(shellHistory) means "not browsing".
	shellHistory []string
	shellHistIdx int
	shellDraft   string
	// probing is true between a script starting and finishing, while the repo
	// fingerprint probe is armed; probeRun is the outputRun the live chain is
	// tagged with. Each tick re-arms itself, so two chains for the SAME run would
	// double the stat rate forever — but a new run must still arm its own,
	// because the in-flight tick carries the superseded run and is dropped
	// without re-arming. Hence a run-tagged guard rather than a bare bool.
	probing  bool
	probeRun int

	// Debounced context loading. Moving the repo cursor loads the newly-highlighted
	// repo's branches + graph, which is 2-3 git subprocesses — cheap once, but a
	// held j through 30 repos fires 30 of them (all but the last discarded as
	// stale) and buries the render loop. A move that lands after a quiet gap is
	// deliberate and loads immediately; moves that land mid-sweep only schedule,
	// and each supersedes the last. Same gen-counter idiom as newsDebounce.
	ctxGen     int       // bumped per move; a tick with a stale gen is dropped
	ctxPending bool      // a deferred load is scheduled and hasn't fired yet
	lastCtxAt  time.Time // when the highlighted repo last changed

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
	newsOffset   int       // scroll offset for the full-screen news overlay (n)
	newsGen      int       // bumped per refresh; guards stale refreshes/ticks
	newsDebounce int       // bumped on each fetch; the latest debounce tick refreshes
	newsLoading  bool      // a summarize is in flight (shows "summarizing..." in the top bar)
	newsCachedAt time.Time // when the current headlines were summarized; gates re-summarizing (newsTTL)

	// post-update changelog (shown once when the app was launched by our own
	// self-updater): the flattened release notes, a scroll offset, and the version
	// we updated from (for the "you were here" marker and the seen-once record).
	changelog       []string
	changelogOffset int
	changelogFrom   string

	// PRs view (key 4, in the top-right slot beside Branches): GitHub pull requests
	// via the gh CLI. Two lists — mine and review-requested — toggled by `m`;
	// prCursor indexes the visible (filtered) list. Loaded async after gh is
	// probed; shows a hint when gh is absent.
	prMine       []gh.PullRequest
	prReview     []gh.PullRequest
	prShowReview bool // false = my open PRs, true = review requested of me
	// prChosen records that the user picked a list with `m`. Until they do, the
	// pane opens on whichever list has something in it (autoPickPRList); after
	// they do, that choice stands and a background refresh can't move it.
	prChosen bool
	prCursor int
	prLoaded bool  // both lists have returned at least once
	prErr    error // last load error (e.g. gh too old for `search prs`)

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

	// `:` harness mode. aiPrompt is the sentence being typed; aiNames is the
	// completion vocabulary (repos, groups, branches) snapshotted when `:` opens,
	// so the ghost text can't shift under a background fetch. Deliberately NOT
	// reusing filter/filtering: those narrow every visible list, and typing a
	// sentence would empty the panes behind the prompt.
	aiPrompting bool
	aiPrompt    string
	aiNames     []string
	// The ghost completion is debounced like the repo cursor is: while you are
	// still typing it stays hidden, because a suggestion that changes length and
	// blinks out on every keystroke reads as the line stuttering. It costs
	// nothing to compute (measured: identical at 40 and 2000 names) — this is
	// about the churn, not the clock.
	aiGhost    bool // show the completion; set once typing settles
	aiGhostGen int  // bumped per keystroke; a stale tick is ignored

	// Prompt history, up/down. Deliberately in-memory only: it is a scratch
	// convenience for the session you are in, and persisting half-finished
	// instructions about someone's repos to disk is not worth the surprise.
	// aiHistIdx == len(aiHistory) means "not browsing"; aiDraft holds what was
	// being typed when browsing started, so down-arrow can put it back.
	aiHistory []string
	aiHistIdx int
	aiDraft   string
	aiRun     int // bumped per request; replies from a superseded one are dropped

	// A validated plan waiting on y/N. Held whole so the confirm shows exactly
	// what will run, and so nothing can be executed that wasn't displayed.
	confirmPlan bool
	pendingPlan aigit.Plan
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

// New builds a Model from discovered repos and scripts. root is the directory
// they were found under — the settings face's scan-depth setting re-walks it.
func New(cfg config.Config, root string, repos []discover.Repo, scripts []discover.Script) Model {
	vms := make([]*repoVM, len(repos))
	for i, r := range repos {
		vms[i] = &repoVM{repo: r}
	}
	conc := cfg.Concurrency
	if conc < 1 {
		conc = 1
	}
	applyTheme(themeByName(cfg.Theme)) // set the themeable styles from config
	m := Model{
		cfg:     cfg,
		root:    root,
		repos:   vms,
		scripts: scripts,
		focus:   panelRepos,
		sem:     make(chan struct{}, conc),
		// topView/bottomView default to their zero values (tvBranches / bvGraph):
		// the top-right shows Branches and the bottom shows Graph on launch.
	}
	// Reuse a fresh on-disk news summary so opening the app doesn't re-summarize
	// every time (see newsTTL). Ignored when it's for a different window or repo set.
	if c, ok := loadNewsCache(); ok && c.Days == cfg.NewsDays && c.Sig == repoSig(vms) && time.Since(c.CachedAt) < newsTTL {
		m.newsFeed = c.Headlines
		m.newsCachedAt = c.CachedAt
	}
	return m
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
	if len(m.newsFeed) > 1 {
		cmds = append(cmds, newsTickCmd(m.newsGen)) // rotate cached headlines
	}
	if c := changelogTriggerCmd(); c != nil {
		cmds = append(cmds, c) // fetch the changelog iff we arrived via a self-update
	}
	return tea.Batch(cmds...)
}
