package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/config"
	"manygit/internal/discover"
	"manygit/internal/git"
)

type panel int

const (
	panelRepos    panel = iota // key 1
	panelScripts               // key 2
	panelBranches              // key 3
	panelBottom                // keys 4/5/6 (multi-view: graph / changes / output)
	panelCount                 // number of focusable panels
)

// bottomView is which view the multi-view bottom-right slot shows.
type bottomView int

const (
	bvGraph   bottomView = iota // key 4
	bvChanges                   // key 5
	bvOutput                    // key 6
	bvAgent                     // key 7
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
	filterPanel     panel // which list `/` filters: panelRepos or panelScripts
	filterAttention bool  // show only repos with changes / ahead / behind
	showHelp        bool  // the settings + help overlay
	showGraph       bool  // full-screen commit graph overlay
	showTagsInline  bool  // show each repo's latest tag inline in the Repos rows (t)
	zoomed          bool  // maximize the focused pane to full screen (z)

	// agent (7): a one-shot AI command helper over the whole workspace, shown in
	// the bottom slot alongside Graph/Changes/Output. agentTyping is the insert
	// mode: while false the pane is navigable (1-7/z work like any other view);
	// pressing enter flips it true to compose an instruction (esc flips it back).
	agentTyping   bool
	agentPhase    agentPhase
	agentInputBuf string
	agentCommands []string
	agentOutput   []string
	agentOffset   int // scroll offset for the output
	agentErr      string

	// settings overlay (?): a cursor over a flat radio-list of choices (each theme,
	// each glyph option, then the editor row); showKeys flips to the keybindings
	// reference; editingOpenCmd/openCmdBuf drive the inline editor edit.
	settingsCursor int
	showKeys       bool
	editingOpenCmd bool
	openCmdBuf     string

	// bottom multi-view slot
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
	newsGen      int // bumped per refresh; guards stale refreshes/ticks
	newsDebounce int // bumped on each fetch; the latest debounce tick refreshes
	newsLoading  bool

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
	return tea.Batch(cmds...)
}
