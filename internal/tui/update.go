package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rabeeh-ta/manygit/internal/aigit"
	"github.com/rabeeh-ta/manygit/internal/config"
	"github.com/rabeeh-ta/manygit/internal/discover"
	"github.com/rabeeh-ta/manygit/internal/git"
	"github.com/rabeeh-ta/manygit/internal/harness"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.FocusMsg:
		// Terminal window regained focus — refresh every repo (like `r`), but
		// only if we haven't fetched recently, so rapid alt-tabbing doesn't spray
		// git fetches at every remote.
		if !m.lastFetch.IsZero() && time.Since(m.lastFetch) < focusRefetchCooldown {
			return m, nil
		}
		m.lastFetch = time.Now()
		return m, m.refetchAllCmd()
	case statusMsg:
		// Whose row the cursor is on BEFORE the status lands — the new status can
		// change which repos are visible, and the cursor is an index into that
		// list, not a repo identity.
		was := ""
		if r := m.currentVisible(m.visibleRepos()); r != nil {
			was = r.repo.Path
		}
		for _, r := range m.repos {
			if r.repo.Path == msg.path {
				r.status = msg.st
				r.loaded = true
				break
			}
		}
		return m, m.reclampCursor(was)
	case fetchDoneMsg:
		var cmds []tea.Cmd
		for _, r := range m.repos {
			if r.repo.Path == msg.path {
				r.fetching = false
				if msg.err == nil {
					cmds = append(cmds, statusCmd(msg.path)) // refresh ahead/behind asynchronously
				}
				break
			}
		}
		// Debounce a news refresh: only the latest tick in a fetch burst refreshes.
		m.newsDebounce++
		cmds = append(cmds, newsDebounceCmd(m.newsDebounce))
		return m, tea.Batch(cmds...)
	case ctxDebounceMsg:
		// Drop a superseded tick — a later move already scheduled its own, and
		// bubbletea gives no way to cancel the one this came from.
		if msg.gen != m.ctxGen || !m.ctxPending {
			return m, nil
		}
		m.ctxPending = false
		return m, m.loadContextCmd()
	case newsDebounceMsg:
		if msg.gen == m.newsDebounce {
			return m, m.maybeRefreshNews()
		}
		return m, nil
	case newsFeedMsg:
		if msg.gen == m.newsGen {
			m.newsLoading = false
			if msg.err == nil {
				m.newsFeed = msg.headlines
				m.newsIndex = 0
				// Stamp the refresh so it isn't re-summarized for newsTTL, and
				// persist non-empty headlines so a restart reuses them too.
				m.newsCachedAt = time.Now()
				if len(msg.headlines) > 0 {
					saveNewsCache(cachedNews{
						CachedAt:  m.newsCachedAt,
						Days:      m.cfg.NewsDays,
						Sig:       repoSig(m.repos),
						Format:    newsFormat,
						Headlines: msg.headlines,
					})
				}
			}
			if len(m.newsFeed) > 1 {
				return m, newsTickCmd(m.newsGen)
			}
		}
		return m, nil
	case newsTickMsg:
		if msg.gen == m.newsGen && len(m.newsFeed) > 1 {
			m.newsIndex = (m.newsIndex + 1) % len(m.newsFeed)
			return m, newsTickCmd(m.newsGen)
		}
		return m, nil
	case syncDoneMsg:
		exp := m.setStatus(m.syncResultText(msg))
		cmds := []tea.Cmd{exp}
		if !msg.skipped && msg.err == nil {
			cmds = append(cmds, statusCmd(msg.path)) // refresh status after a successful sync
		}
		return m, tea.Batch(cmds...)
	case pushDoneMsg:
		name := baseName(msg.path)
		var s string
		switch {
		case msg.skipped:
			s = styleOrange.Render("push " + name + " skipped: " + msg.reason)
		case msg.err != nil:
			s = styleRed.Render("push " + name + " failed: " + msg.err.Error())
		default:
			s = styleGreen.Render("pushed " + name)
		}
		exp := m.setStatus(s)
		if msg.skipped {
			return m, exp // nothing changed; no need to re-stat the repo
		}
		return m, tea.Batch(exp, statusCmd(msg.path))
	case openDoneMsg:
		if msg.err != nil {
			return m, m.setStatus(styleRed.Render("open " + baseName(msg.path) + " failed: " + msg.err.Error()))
		}
		return m, nil
	case discardDoneMsg:
		name := baseName(msg.path)
		if msg.err != nil {
			return m, m.setStatus(styleRed.Render("discard " + name + " failed: " + msg.err.Error()))
		}
		what := "tracked changes"
		if msg.full {
			what = "all changes"
		}
		exp := m.setStatus(styleGreen.Render("discarded " + what + " in " + name))
		// Refresh the repo's dirty count and the visible panels (graph/changes).
		return m, tea.Batch(exp, statusCmd(msg.path), m.loadContextCmd())
	case checkoutDoneMsg:
		name := baseName(msg.path)
		if msg.err != nil {
			exp := m.setStatus(styleRed.Render("checkout " + name + " failed: " + msg.err.Error()))
			return m, exp
		}
		exp := m.setStatus(styleGreen.Render("checked out " + msg.branch + " in " + name))
		return m, tea.Batch(exp, statusCmd(msg.path), m.loadContextCmd())
	case branchesMsg:
		if r := m.currentVisible(m.visibleRepos()); r != nil && r.repo.Path == msg.path {
			m.branches = msg.branches
			if m.branchCursor >= len(m.visibleBranches()) {
				m.branchCursor = 0
			}
		}
		return m, nil
	case ghProbeMsg:
		m.ghProbed = true
		m.ghInstalled = msg.installed
		m.ghAvailable = msg.available
		m.ghUser = msg.user
		if msg.available {
			return m, tea.Batch(myPRsCmd(), reviewPRsCmd()) // now load both PR lists
		}
		return m, nil
	case prsMsg:
		if msg.err == nil {
			if msg.review {
				m.prReview = msg.prs
			} else {
				m.prMine = msg.prs
			}
		}
		m.prLoaded = true
		m.prErr = msg.err
		m.autoPickPRList() // the lists arrive after `4` is usually pressed
		if m.prCursor >= len(m.visiblePRs()) {
			m.prCursor = 0
		}
		return m, nil
	case prCheckoutDoneMsg:
		name := baseName(msg.path)
		num := strconv.Itoa(msg.number)
		if msg.err != nil {
			return m, m.setStatus(styleRed.Render("checkout PR #" + num + " in " + name + " failed: " + msg.err.Error()))
		}
		exp := m.setStatus(styleGreen.Render("checked out PR #" + num + " in " + name))
		// Deliberately nothing moves: a PR spans several repos and you check out
		// two or three in a row, so stealing focus to that repo's Branches pane
		// would break the walk. statusCmd redraws its row in place instead.
		return m, tea.Batch(exp, statusCmd(msg.path), m.loadContextIfCurrent(msg.path))
	case changelogMsg:
		// Fail soft: an error, or no releases with any notes, just means no
		// screen — the app is already up behind it. Mark it seen either way so a
		// transient fetch failure doesn't re-nag on every launch.
		if msg.err == nil && len(msg.releases) > 0 {
			lines := changelogLines(msg.releases, msg.from)
			if len(lines) > 0 {
				m.changelog = lines
				m.changelogFrom = msg.from
				m.changelogOffset = 0
				m.showChangelog = true
			}
		}
		markChangelogSeen(msg.from)
		return m, nil
	case rescanMsg:
		switch {
		case msg.err != nil:
			return m, m.setStatus(styleRed.Render("rescan failed: " + msg.err.Error()))
		case len(msg.repos) == 0:
			// Keep the old depth AND the old list: an empty Repos pane is a state
			// main.go won't even start in, so `?` must not be able to produce it.
			return m, m.setStatus(styleOrange.Render(fmt.Sprintf(
				"no repos at depth %d — staying at %d", msg.depth, m.cfg.MaxDepth)))
		}
		m.cfg.MaxDepth = msg.depth // commit only now that the walk paid off
		m.saveConfig()
		return m, m.applyRescan(msg.repos)
	case latestTagMsg:
		for _, r := range m.repos {
			if r.repo.Path == msg.path {
				r.latestTag = msg.tag
				break
			}
		}
		return m, nil
	case graphMsg:
		if r := m.currentVisible(m.visibleRepos()); r != nil && r.repo.Path == msg.path {
			m.graphLines = make([]string, len(msg.lines))
			for i, ln := range msg.lines {
				m.graphLines[i] = shortenGraphRefs(ln) // cap long ref names in decorations
			}
			m.graphCommits = msg.commits
			m.graphSel = 0
			m.graphOffset = 0
		}
		return m, nil
	case changesMsg:
		if r := m.currentVisible(m.visibleRepos()); r != nil && r.repo.Path == msg.path {
			m.changeFiles = msg.files
			m.changeCursor = 0
			m.changeShowDiff = false
		}
		return m, nil
	case diffMsg:
		// Drop a stale diff (repo or graph selection changed while it loaded).
		if r := m.currentVisible(m.visibleRepos()); r != nil && r.repo.Path == msg.path && m.selectedRef() == msg.ref {
			m.changeDiff = msg.lines
			m.changeDiffOff = 0
			m.changeShowDiff = true
		}
		return m, nil
	case aiGhostMsg:
		if msg.gen == m.aiGhostGen && m.aiPrompting {
			m.aiGhost = true
		}
		return m, nil

	case aiPlanMsg:
		if msg.run != m.aiRun {
			return m, nil // a superseded request; its reply is no longer wanted
		}
		m.outputRunning = false
		m.outputLines = nil
		m.outputOffset = 0
		m.appendOutput("") // top margin: the block shouldn't hug the border
		if msg.err != nil {
			m.appendAI(styleRed.Render("harness error"))
			m.appendAI(styleDim.Render(msg.err.Error()))
			return m, m.setStatus("no plan — see Output")
		}
		if msg.plan.Note != "" {
			m.appendAI(styleDim.Render(msg.plan.Note))
		}
		// Refused steps never reach the confirm: showing a plan and then blocking
		// half of it on y would be worse than refusing it whole.
		if bad := aigit.Validate(msg.plan); len(bad) > 0 {
			m.appendAI(styleRed.Render("refused — not run"))
			for _, r := range bad {
				m.appendAI(r.Step.Repo + "  " + r.Step.Command())
				m.appendAI(styleDim.Render("  ↳ " + r.Reason))
			}
			return m, m.setStatus("plan refused")
		}
		if len(msg.plan.Steps) == 0 {
			if msg.plan.Note == "" {
				m.appendAI(styleDim.Render("nothing to do"))
			}
			// Each request is answered once and nothing is carried over, so an
			// empty plan is the end of the exchange, not a pause in one. Say how
			// to start another rather than leaving the user waiting for a reply.
			m.appendOutput("")
			m.appendAI(styleDim.Render("press : to ask again"))
			return m, nil
		}
		for _, s := range msg.plan.Steps {
			m.appendAI(styleGroup.Render(s.Repo) + "  " + s.Command())
		}
		m.appendOutput("")
		m.appendAI(styleYellow.Render("run " + plural(len(msg.plan.Steps), "command") + "? [y/N]"))
		m.confirmPlan = true
		m.pendingPlan = msg.plan
		return m, nil

	case aiDoneMsg:
		if msg.run != m.aiRun {
			return m, nil
		}
		m.outputRunning = false
		m.probing = false // the next probe tick finds it disarmed and stops
		ok, failed := 0, 0
		for _, r := range msg.results {
			switch {
			case r.Skipped:
				m.appendAI(styleDim.Render(r.Step.Repo + "  skipped"))
			case r.Err != nil:
				failed++
				m.appendAI(styleGroup.Render(r.Step.Repo) + "  " + r.Step.Command() + "  " + styleRed.Render("FAILED"))
				m.appendAI(styleDim.Render("  " + r.Err.Error()))
			default:
				ok++
				m.appendAI(styleGroup.Render(r.Step.Repo) + "  " + r.Step.Command() + "  " + styleGreen.Render("ok"))
			}
		}
		// Whatever ran changed the repos, so re-read them rather than leaving the
		// list showing pre-command branches and counts.
		if failed > 0 {
			return m, tea.Batch(m.setStatus("stopped after a failure — see Output"), m.refetchAllCmd())
		}
		return m, tea.Batch(m.setStatus(plural(ok, "command")+" ok"), m.refetchAllCmd())

	case repoProbeMsg:
		// Only the current run's ticks count, and only while the probe is armed —
		// bubbletea can't cancel a pending Tick, so declining to re-arm is what
		// stops it.
		if msg.run != m.outputRun || !m.probing {
			return m, nil
		}
		cmds := []tea.Cmd{repoProbeCmd(m.outputRun, m.repoPaths())}
		for _, path := range m.probeChanged(msg.fps) {
			cmds = append(cmds, statusCmd(path), m.loadContextIfCurrent(path))
		}
		return m, tea.Batch(cmds...)
	case runStartMsg:
		// The pane may already have moved on — takeOutputPane could not have
		// killed this one, because the process did not exist yet when it ran.
		if msg.run != m.outputRun {
			msg.cancel()
			return m, nil
		}
		m.shellCancel = msg.cancel
		return m, readScriptLine(msg.scanner, msg.run)
	case scriptOutMsg:
		stale := msg.run != m.outputRun // a superseded run (user started another script)
		if msg.done {
			if stale {
				return m, nil // superseded run finished draining; drop it silently
			}
			m.outputRunning = false
			m.probing = false // the next tick finds it disarmed and stops
			m.shellCancel = nil
			var s string
			switch {
			case m.shellKilled:
				s = styleOrange.Render(m.cancelledText())
			case m.outputKind == outShell && msg.err != nil:
				s = styleRed.Render(m.shellLoc + ": " + exitText(msg.err))
			case m.outputKind == outShell:
				s = styleGreen.Render(m.shellLoc + ": " + exitText(nil))
			case msg.err != nil:
				s = styleRed.Render("script " + m.outputTitle + " failed: " + msg.err.Error())
			default:
				s = styleGreen.Render("ran " + m.outputTitle)
			}
			// A script can change anything, in any repo — and it can dirty a tree
			// without ever invoking git, which the fingerprint probe is blind to by
			// design. So the run ends with one full local re-stat rather than
			// leaving the pane stale until someone presses r.
			return m, tea.Batch(m.setStatus(s), m.restatAllCmd(), m.loadContextCmd())
		}
		if !stale {
			m.appendOutput(msg.line)
		}
		// Keep reading even a superseded run so its process drains and exits.
		return m, readScriptLine(msg.scanner, msg.run)
	case escapeExpireMsg:
		if m.lastEscape.Equal(msg.pressedAt) {
			m.lastEscape = time.Time{}
		}
		return m, nil
	case statusExpireMsg:
		if msg.gen == m.statusGen {
			m.statusLine = ""
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// rescanCmd re-walks the root at depth, off the keystroke. It reports the depth
// back untouched so the handler can commit it only if the walk paid off.
func (m Model) rescanCmd(depth int) tea.Cmd {
	root, prune := m.root, m.cfg.PruneSet()
	return func() tea.Msg {
		repos, err := discover.Discover(root, discover.Options{MaxDepth: depth, Prune: prune})
		return rescanMsg{depth: depth, repos: repos, err: err}
	}
}

// applyRescan swaps in a freshly discovered repo set. Repos that were already on
// screen keep their loaded *repoVM — their status hasn't changed just because the
// walk got wider, and re-stat'ing them would blank the list and re-fetch every
// remote. Only genuinely new repos are stat'd and fetched.
func (m *Model) applyRescan(found []discover.Repo) tea.Cmd {
	prev := make(map[string]*repoVM, len(m.repos))
	for _, r := range m.repos {
		prev[r.repo.Path] = r
	}
	vms := make([]*repoVM, len(found))
	var cmds []tea.Cmd
	added := 0
	for i, rp := range found {
		if old, ok := prev[rp.Path]; ok {
			vms[i] = old
			continue
		}
		vm := &repoVM{repo: rp, fetching: true}
		vms[i] = vm
		added++
		cmds = append(cmds, statusCmd(rp.Path), fetchCmd(m.sem, rp.Path))
	}
	dropped := len(m.repos) - (len(found) - added)
	m.repos = vms
	m.cursor = 0
	m.clearBranchFilter()
	cmds = append(cmds,
		m.setStatus(styleGreen.Render(fmt.Sprintf("depth %d: %d repos (+%d, -%d)",
			m.cfg.MaxDepth, len(found), added, dropped))),
		m.loadContextCmd())
	return tea.Batch(cmds...)
}

// loadTagsCmd loads the latest tag for every repo (fast local reads, ungated).
func (m Model) loadTagsCmd() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.repos))
	for _, r := range m.repos {
		cmds = append(cmds, latestTagCmd(r.repo.Path))
	}
	return tea.Batch(cmds...)
}

// ctxSettle is how long the repo cursor must sit still before a move counts as
// deliberate rather than part of a key-repeat sweep. Key autorepeat delivers
// every ~30ms, so this comfortably spans a burst while staying under the ~100ms
// that reads as instant when you stop on a row.
const ctxSettle = 120 * time.Millisecond

// ctxDebounceCmd schedules a context load ctxSettle from now; a later move
// supersedes it by bumping ctxGen, so only the final one of a sweep loads.
func ctxDebounceCmd(gen int) tea.Cmd {
	return tea.Tick(ctxSettle, func(time.Time) tea.Msg { return ctxDebounceMsg{gen: gen} })
}

// ghostSettle is how long the `:` prompt must sit still before the completion is
// offered. Same idea and same interval as ctxSettle: mid-burst suggestions are
// noise, a pause means you have stopped to think.
const ghostSettle = 120 * time.Millisecond

// aiGhostMsg reveals the completion if no key has landed since it was scheduled.
type aiGhostMsg struct{ gen int }

func aiGhostCmd(gen int) tea.Cmd {
	return tea.Tick(ghostSettle, func(time.Time) tea.Msg { return aiGhostMsg{gen: gen} })
}

// contextCmd is loadContextCmd for moves the user drives continuously — the
// repo cursor (j/k) and typing a repo filter, both of which re-pick the
// highlighted repo on every keystroke.
//
// A move that lands after a quiet gap is deliberate, so it loads immediately and
// navigation stays instant. A move that lands mid-sweep only schedules a load
// and supersedes whatever was already pending, so running down 30 rows costs one
// load at the start and one when you stop, not one per row.
func (m *Model) contextCmd() tea.Cmd {
	now := time.Now()
	quiet := now.Sub(m.lastCtxAt) >= ctxSettle
	m.lastCtxAt = now
	m.ctxGen++ // supersede any tick already in flight, whichever branch we take
	if quiet {
		m.ctxPending = false
		return m.loadContextCmd()
	}
	m.ctxPending = true
	return ctxDebounceCmd(m.ctxGen)
}

// loadContextCmd loads branches + the commit graph for the highlighted repo
// (and refreshes the Changes view when it's the one on screen).
func (m Model) loadContextCmd() tea.Cmd {
	r := m.currentVisible(m.visibleRepos())
	if r == nil {
		return nil
	}
	cmds := []tea.Cmd{branchesCmd(r.repo.Path), graphCmd(r.repo.Path, 200)}
	// The graph resets to WIP on reload, so keep a visible Changes view (5) in
	// step by refreshing it to the new repo's working-tree changes — otherwise it
	// stays stuck on the repo it was opened on while you browse others.
	if m.bottomView == bvChanges {
		cmds = append(cmds, changesCmd(r.repo.Path, ""))
	}
	return tea.Batch(cmds...)
}

// selectedRef returns the git ref the graph cursor is on: "" for WIP (working
// tree), otherwise the selected commit's hash.
func (m Model) selectedRef() string {
	if m.graphSel <= 0 || m.graphSel-1 >= len(m.graphCommits) {
		return ""
	}
	return m.graphCommits[m.graphSel-1].Hash
}

// loadChangesCmd loads the changed files of the currently-selected graph entry.
func (m Model) loadChangesCmd() tea.Cmd {
	r := m.currentVisible(m.visibleRepos())
	if r == nil {
		return nil
	}
	return changesCmd(r.repo.Path, m.selectedRef())
}

// runScriptCmd starts the highlighted script in the background, streaming its
// combined output into the Output view (6). nil if no script is selected.
func (m Model) runScriptCmd() tea.Cmd {
	vs := m.visibleScripts()
	if m.scriptCursor < 0 || m.scriptCursor >= len(vs) {
		return nil
	}
	return startScriptCmd(vs[m.scriptCursor].Path, m.outputRun)
}

// focusRefetchCooldown is the minimum gap between terminal-focus refetches; a
// manual `r` refresh is never gated by it (and resets the clock).
const focusRefetchCooldown = 45 * time.Second

// refetchAllCmd fetches every not-already-fetching repo (the `r` action, also
// fired when the terminal window regains focus).
func (m Model) refetchAllCmd() tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range m.repos {
		if r.fetching {
			continue
		}
		r.fetching = true
		cmds = append(cmds, fetchCmd(m.sem, r.repo.Path))
	}
	return tea.Batch(cmds...)
}

// aiIndent is the left margin every `:` line shares. Script output is raw and
// starts at the edge, but the AI block is a formatted report and reads better
// inset — as long as EVERY line agrees. Mixing indented steps with flush-left
// headings is what made the pane look ragged.
const aiIndent = "  "

// appendAI adds one line of the `:` report at the shared margin.
func (m *Model) appendAI(line string) { m.appendOutput(aiIndent + line) }

// appendOutput adds a line to the Output view, keeping the view pinned to the
// tail (auto-follow) unless the user has scrolled up.
func (m *Model) appendOutput(line string) {
	atBottom := m.outputOffset >= len(m.outputLines)-1
	m.outputLines = append(m.outputLines, line)
	if atBottom {
		m.outputOffset = len(m.outputLines) - 1
	}
}

const doubleEscapeWindow = 500 * time.Millisecond

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleKeyAt(msg, time.Now())
}

// Keep the clock explicit so the double-Escape boundary can be tested without sleeps.
func (m Model) handleKeyAt(msg tea.KeyMsg, now time.Time) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		if !m.lastEscape.IsZero() && now.Sub(m.lastEscape) >= 0 && now.Sub(m.lastEscape) <= doubleEscapeWindow {
			return m, tea.Quit
		}
		m.lastEscape = now
		next, cmd := m.handleSingleKey(msg)
		expire := tea.Tick(doubleEscapeWindow, func(time.Time) tea.Msg {
			return escapeExpireMsg{pressedAt: now}
		})
		return next, tea.Batch(cmd, expire)
	} else {
		m.lastEscape = time.Time{}
	}
	return m.handleSingleKey(msg)
}

func (m Model) handleSingleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	// Above the overlay guards for the same reason aiPrompting is: a `?` or `g`
	// inside a shell command is part of the command, not a request for an overlay.
	if m.shellPrompting {
		return m.handleShellPromptKey(msg)
	}
	// Above the overlay guards on purpose: a stray ? or g while you are typing a
	// sentence must land in the sentence, not open a full-screen overlay.
	if m.aiPrompting {
		return m.handleAIPromptKey(msg)
	}
	if m.showHelp {
		return m.handleSettingsKey(msg)
	}
	// Above confirmDiscard, which swallows any key unconditionally — this keeps
	// ownership of y/N deterministic if both were ever armed at once.
	if m.confirmPlan {
		plan := m.pendingPlan
		m.confirmPlan = false
		m.pendingPlan = aigit.Plan{}
		if msg.String() == "y" {
			m.takeOutputPane(outAI, "running "+plural(len(plan.Steps), "command"))
			m.setBottomView(bvOutput)
			// These are git commands against the repos, so the Repos pane follows
			// them live exactly as it follows a script.
			return m, tea.Batch(aiExecCmd(m.root, m.repoDirs(), plan, m.aiRun), m.startRepoProbe())
		}
		m.appendAI(styleDim.Render("cancelled — nothing ran"))
		return m, m.setStatus("plan cancelled")
	}
	if m.confirmDiscard {
		full, path, name := m.confirmDiscardFull, m.confirmDiscardPath, m.confirmDiscardName
		m.confirmDiscard = false
		if msg.String() == "y" {
			return m, tea.Batch(m.setStatus("discarding "+name+"..."), discardCmd(m.sem, path, full))
		}
		return m, m.setStatus("discard cancelled")
	}
	if m.showGraph {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "g", "esc":
			m.showGraph = false
		case "down", "j":
			if m.graphOffset < len(m.graphLines)-1 {
				m.graphOffset++
			}
		case "up", "k":
			if m.graphOffset > 0 {
				m.graphOffset--
			}
		}
		return m, nil
	}
	if m.showNews {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "n", "esc":
			m.showNews = false
		case "down", "j":
			// By RENDERED line, not by headline: an entry is a heading plus a
			// wrapped explanation plus a gap, so clamping to len(newsFeed) would
			// stop j well short of the bottom.
			if m.newsOffset < len(m.newsLines())-1 {
				m.newsOffset++
			}
		case "up", "k":
			if m.newsOffset > 0 {
				m.newsOffset--
			}
		}
		return m, nil
	}
	if m.showChangelog {
		// The one-time post-update screen: scroll with j/k/arrows, esc (or q)
		// dismisses into the app. It sits above everything; once closed it can't
		// be reopened (the seen marker is already written), which is why there's
		// no key to toggle it back on — it is a "here's what's new" splash, not a
		// pane you navigate to.
		switch msg.String() {
		case "esc", "q", "enter", " ":
			m.showChangelog = false
		case "down", "j":
			if m.changelogOffset < len(m.changelog)-1 {
				m.changelogOffset++
			}
		case "up", "k":
			if m.changelogOffset > 0 {
				m.changelogOffset--
			}
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}
	vis := m.visibleRepos()
	switch msg.String() {
	case "ctrl+c":
		// Shell-like: while something is running, ctrl+c stops it rather than
		// quitting. `q` always quits, so this can never trap anyone.
		if m.outputRunning && m.shellCancel != nil {
			m.killRunning()
			return m, m.setStatus(styleOrange.Render(m.cancelledText()))
		}
		return m, tea.Quit
	case "q":
		return m, tea.Quit
	case "?":
		// ? is the universal "show me the keys" reflex, so it lands on the
		// keybindings. Settings is the overlay's other face, reached from inside
		// with tab / shift+tab / [ / ] — it has no key of its own.
		m.showHelp = true
		m.showKeys = true
	case "z":
		// Maximize the focused pane to full screen (toggle); zoom follows focus.
		m.zoomed = !m.zoomed
	case "g":
		// Full-screen colored commit graph (reuses the loaded graph).
		m.showGraph = true
		m.graphOffset = 0
	case "n":
		// Full-screen news feed: every headline at once (toggle; n/esc closes).
		m.showNews = true
		m.newsOffset = 0
		if len(m.newsFeed) == 0 { // nothing yet — kick off a summary if we can
			return m, m.maybeRefreshNews()
		}
	case "t":
		// Toggle each repo's latest tag inline in the Repos rows (after the
		// branch). Off by default; loading the tags happens when switched on.
		// The tag is part of what `/` matches, so with a repo filter active this
		// resizes the visible list under the cursor — pin it to its repo first.
		cur := ""
		if r := m.currentVisible(vis); r != nil {
			cur = r.repo.Path
		}
		m.showTagsInline = !m.showTagsInline
		var cmds []tea.Cmd
		if m.showTagsInline {
			cmds = append(cmds, m.loadTagsCmd())
		}
		if m.filter != "" && m.filterPanel == panelRepos {
			cmds = append(cmds, m.keepCursorOn(cur))
		}
		return m, tea.Batch(cmds...)
	case "1":
		m.focus = panelRepos
	case "2":
		m.focus = panelScripts
	case "3":
		m.setTopView(tvBranches)
	case "4":
		m.setTopView(tvPRs)
	case "5":
		return m, m.setBottomView(bvGraph)
	case "6":
		return m, m.setBottomView(bvChanges)
	case "7":
		return m, m.setBottomView(bvOutput)
	case "]":
		return m, m.cycleTab(1)
	case "[":
		return m, m.cycleTab(-1)
	case "tab":
		m.focus = (m.focus + 1) % panelCount
	case "shift+tab":
		// + panelCount before the modulo: Go's % keeps the sign of the dividend,
		// so focus 0 - 1 would be -1, not the last panel.
		m.focus = (m.focus - 1 + panelCount) % panelCount
	case "right":
		// →/← hop between the two panels you actually browse together: Repos and
		// the highlighted repo's Branches. Deliberately scoped to those two — from
		// any other panel the arrows stay unbound, leaving them free for
		// panel-local uses (e.g. scrolling a wide diff) later.
		if m.focus == panelRepos {
			m.setTopView(tvBranches) // → always shows the highlighted repo's branches
			m.branchCursor = 0
		}
	case "left":
		if m.focus == panelBranches {
			m.focus = panelRepos
		}
	case "down", "j":
		// Navigate within the FOCUSED panel (repos vs. branches/PRs), so browsing
		// doesn't move the repo cursor and reload the panels.
		switch m.focus {
		case panelRepos:
			if m.cursor < len(vis)-1 {
				m.cursor++
				m.clearBranchFilter() // the branch filter belonged to the old repo
				return m, m.contextCmd()
			}
		case panelBranches:
			m.topScroll(1)
		case panelScripts:
			if m.scriptCursor < len(m.visibleScripts())-1 {
				m.scriptCursor++
			}
		case panelBottom:
			m.bottomScroll(1)
		}
	case "up", "k":
		switch m.focus {
		case panelRepos:
			if m.cursor > 0 {
				m.cursor--
				m.clearBranchFilter() // the branch filter belonged to the old repo
				return m, m.contextCmd()
			}
		case panelBranches:
			m.topScroll(-1)
		case panelScripts:
			if m.scriptCursor > 0 {
				m.scriptCursor--
			}
		case panelBottom:
			m.bottomScroll(-1)
		}
	case "J":
		if m.focus == panelBranches && m.topView == tvBranches && m.branchCursor < len(m.visibleBranches())-1 {
			m.branchCursor++
		}
	case "K":
		if m.focus == panelBranches && m.topView == tvBranches && m.branchCursor > 0 {
			m.branchCursor--
		}
	case "enter":
		// enter is the single selection key everywhere: Repos → drill into the
		// repo's branches, Branches → checkout, PRs → checkout the PR's branch,
		// Scripts → run, Graph/Changes → drill.
		// Graph → drill into the selected commit/WIP's changed files.
		if m.focus == panelBottom && m.bottomView == bvGraph {
			m.bottomView = bvChanges
			m.changeShowDiff = false
			return m, m.loadChangesCmd()
		}
		// Changes → open the highlighted file's diff in-place.
		if m.focus == panelBottom && m.bottomView == bvChanges && !m.changeShowDiff {
			if r := m.currentVisible(vis); r != nil && m.changeCursor < len(m.changeFiles) {
				return m, diffCmd(r.repo.Path, m.selectedRef(), m.changeFiles[m.changeCursor].Path)
			}
			return m, nil
		}
		switch m.focus {
		case panelRepos:
			// Jump into the highlighted repo's branches.
			m.setTopView(tvBranches)
			m.branchCursor = 0
			return m, nil
		case panelScripts:
			return m, m.runSelectedScript()
		case panelBranches:
			if m.topView == tvPRs {
				return m, m.checkoutPR() // checkout the highlighted PR's branch
			}
		}
		return m, m.checkoutSelected(vis) // Branches → checkout the highlighted branch
	case "b":
		cmd := m.checkoutSelected(vis)
		return m, cmd
	case "m":
		// Toggle the PRs sub-view between "my PRs" and "review requests". Dedicated
		// key, scoped to the PRs view so it stays unbound everywhere else.
		if m.focus == panelBranches && m.topView == tvPRs {
			m.prShowReview = !m.prShowReview
			m.prChosen = true // an explicit pick; autoPickPRList stops overriding
			m.prCursor = 0
		}
	case "esc":
		// Back out one layer per press, innermost first — diff, then Changes, then
		// zoom, then the filters (which clear last, being what you most likely
		// still want). One case fires per press; the switch's order is the nesting.
		switch {
		case m.outputRunning && m.shellCancel != nil &&
			m.focus == panelBottom && m.bottomView == bvOutput:
			// Scoped to the Output pane on purpose: esc's job everywhere else is
			// "back out one layer", and killing a background command from inside a
			// diff would be a nasty surprise. ctrl+c is the unscoped one.
			m.killRunning()
			return m, m.setStatus(styleOrange.Render(m.cancelledText()))
		case m.focus == panelBottom && m.bottomView == bvChanges && m.changeShowDiff:
			m.changeShowDiff = false
		case m.focus == panelBottom && m.bottomView == bvChanges:
			m.bottomView = bvGraph
		case m.zoomed:
			m.zoomed = false
		case m.filter != "":
			// Land back on the repo you filtered your way to, rather than snapping
			// to the top of the widened list — keepCursorOn also skips the context
			// reload when the cursor didn't actually move.
			cur := ""
			if r := m.currentVisible(vis); r != nil {
				cur = r.repo.Path
			}
			m.filter = ""
			m.filterPanel = panelRepos
			m.branchCursor = 0
			m.prCursor = 0
			m.scriptCursor = 0
			return m, m.keepCursorOn(cur)
		case m.filterAttention:
			cur := ""
			if r := m.currentVisible(vis); r != nil {
				cur = r.repo.Path
			}
			m.filterAttention = false
			return m, m.keepCursorOn(cur)
		}
	case "o":
		path, missing := m.openTarget()
		switch {
		case missing != "":
			return m, m.setStatus(styleOrange.Render(noLocalClone(missing, "open")))
		case path != "":
			return m, openRepoCmd(m.cfg.OpenCmd, path)
		}
	case "F":
		// Toggle the "needs attention" view: only repos with changes / ahead / behind.
		m.filterAttention = !m.filterAttention
		m.cursor = 0
		return m, m.loadContextCmd()
	case "/":
		// The filter is scoped to the sub-view you're on: Repos, Scripts, Branches,
		// or PRs (searching the branch list is the only sane way through a repo's
		// hundreds of remote refs). From the bottom slot it falls back to Repos.
		m.filtering = true
		m.filter = ""
		switch {
		case m.focus == panelScripts:
			m.filterPanel = panelScripts
			m.scriptCursor = 0
		case m.focus == panelBranches && m.topView == tvPRs:
			m.filterPanel = filterPRs
			m.prCursor = 0
		case m.focus == panelBranches:
			m.filterPanel = panelBranches
			m.branchCursor = 0
		default:
			m.filterPanel = panelRepos
			m.cursor = 0
		}
	case "!":
		// A shell in the highlighted repo. `!` is the key, vim's shell-escape
		// convention — but what it OPENS renders as `<repo> $`, because `$` is the
		// prompt character everyone reads as "shell" and it matches the
		// `<repo> $ <cmd>` line the pane echoes back. Key follows the editors,
		// symbol follows the shell.
		//
		// Bound to the cursor repo up front and shown in the prompt, so there is
		// never a question which folder a command lands in, and so a background
		// fetch cannot move it mid-type.
		r := m.currentVisible(vis)
		if r == nil {
			return m, m.setStatus(styleOrange.Render("no repo selected"))
		}
		m.shellPrompting = true
		m.shellCmd = ""
		m.shellLoc = shellLocation(r.repo)
		m.shellDir = r.repo.Path
		m.shellHistIdx = len(m.shellHistory)
	case ":":
		// Plain-English git. Unlike `/` this is global, not scoped to a pane — the
		// request names its own scope, defaulting to the repo under the cursor.
		if !harness.Available(m.cfg.Harness) {
			return m, m.setStatus("no AI harness — install claude or codex, or set one in ? settings")
		}
		m.aiPrompting = true
		m.aiPrompt = ""
		m.aiNames = m.aiContext().Names() // snapshot: ghost text must not shift under a fetch
	case "f":
		if r := m.currentVisible(vis); r != nil && !r.fetching {
			r.fetching = true
			return m, fetchCmd(m.sem, r.repo.Path)
		}
	case "r":
		m.lastFetch = time.Now() // manual refresh resets the focus cooldown
		cmds := []tea.Cmd{m.refetchAllCmd()}
		if m.ghAvailable {
			cmds = append(cmds, myPRsCmd(), reviewPRsCmd()) // refresh PRs too
		}
		return m, tea.Batch(cmds...)
	case "s":
		var cmds []tea.Cmd
		for _, r := range m.targets() {
			if !r.loaded {
				path := r.repo.Path
				cmds = append(cmds, func() tea.Msg {
					return syncDoneMsg{path: path, skipped: true, reason: "status not loaded yet"}
				})
				continue
			}
			// A local-only repo has nothing to pull from: `pull --ff-only` would
			// fail with "no tracking information". Say why instead.
			if !r.status.HasRemote {
				path := r.repo.Path
				cmds = append(cmds, func() tea.Msg {
					return syncDoneMsg{path: path, skipped: true, reason: "no remote"}
				})
				continue
			}
			if r.status.DirtyCount > 0 {
				path := r.repo.Path
				cmds = append(cmds, func() tea.Msg {
					return syncDoneMsg{path: path, skipped: true, reason: "dirty working tree"}
				})
				continue
			}
			cmds = append(cmds, syncCmd(m.sem, r.repo.Path))
		}
		return m, tea.Batch(cmds...)
	case "p":
		var cmds []tea.Cmd
		for _, r := range m.targets() {
			// Until status loads we don't know if there's a remote — skip rather
			// than push blind (a local-only repo would fail "No configured push
			// destination"), mirroring the s handler.
			if !r.loaded {
				path := r.repo.Path
				cmds = append(cmds, func() tea.Msg {
					return pushDoneMsg{path: path, skipped: true, reason: "status not loaded yet"}
				})
				continue
			}
			// No remote: git fails with "No configured push destination" — a skip
			// with the reason is friendlier than a red error.
			if !r.status.HasRemote {
				path := r.repo.Path
				cmds = append(cmds, func() tea.Msg {
					return pushDoneMsg{path: path, skipped: true, reason: "no remote"}
				})
				continue
			}
			cmds = append(cmds, pushCmd(m.sem, r.repo.Path))
		}
		return m, tea.Batch(cmds...)
	case "d":
		return m.armDiscard(vis, false) // discard tracked changes (keep untracked)
	case "D":
		return m.armDiscard(vis, true) // full clean (also delete untracked files)
	}
	return m, nil
}

// armDiscard arms the discard confirmation for the highlighted repo. full=true is
// D (reverts tracked changes AND deletes untracked files); false is d (tracked
// changes only). Nothing runs until the next key confirms with y.
func (m Model) armDiscard(vis []*repoVM, full bool) (tea.Model, tea.Cmd) {
	r := m.currentVisible(vis)
	if r == nil {
		return m, nil
	}
	name := baseName(r.repo.Path)
	if r.loaded && r.status.DirtyCount == 0 {
		return m, m.setStatus("nothing to discard in " + name)
	}
	m.confirmDiscard = true
	m.confirmDiscardFull = full
	m.confirmDiscardPath = r.repo.Path
	m.confirmDiscardName = name
	prompt := "discard changes in " + name + "?  y = confirm, any key = cancel"
	if full {
		prompt = "discard " + name + " + untracked files?  y = confirm, any key = cancel"
	}
	return m, m.setStatus(styleRed.Render(prompt))
}

// handleAIPromptKey drives the `:` input. It switches on msg.Type and ignores
// everything else, the same closed-set approach handleFilterKey uses — that is
// what stops tab from cycling panes, j/k from scrolling, and 1-7 from switching
// panes while you are mid-sentence.
//
// Two deliberate divergences from handleFilterKey, both because this is prose
// rather than a needle:
//
//   - tea.KeySpace is handled. bubbletea re-types a lone space from KeyRunes to
//     KeySpace, so a handler that only takes KeyRunes silently drops every space
//     — which is exactly why `/` cannot contain one. A sentence is mostly spaces.
//   - backspace trims a rune, not a byte, so deleting a non-ASCII character does
//     not leave half of it behind.
func (m Model) handleAIPromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.aiPrompting = false
		m.aiPrompt = ""
		return m, nil
	case tea.KeyTab:
		// tab completes whether or not the ghost is currently shown: it is an
		// explicit request, and the match is the same one the ghost would offer.
		m.aiPrompt += aigit.Complete(m.aiNames, m.aiPrompt)
		return m, m.scheduleGhost()
	case tea.KeyBackspace:
		// alt+backspace deletes a word, the readline convention every shell uses.
		// macOS sends it for opt+backspace; ctrl+w below is the same thing for
		// terminals that don't.
		if msg.Alt {
			m.aiPrompt = dropWord(m.aiPrompt)
		} else if r := []rune(m.aiPrompt); len(r) > 0 {
			m.aiPrompt = string(r[:len(r)-1])
		}
		return m, m.scheduleGhost()
	case tea.KeyCtrlW:
		m.aiPrompt = dropWord(m.aiPrompt)
		return m, m.scheduleGhost()
	case tea.KeyCtrlU:
		// Clear the line. cmd+backspace does this in a text field, but terminals
		// don't transmit cmd at all — they send ^U if configured to send anything,
		// so this is the binding that can actually be reached.
		m.aiPrompt = ""
		return m, m.scheduleGhost()
	case tea.KeyUp:
		return m, m.historyStep(-1)
	case tea.KeyDown:
		return m, m.historyStep(+1)
	case tea.KeyEnter:
		req := strings.TrimSpace(m.aiPrompt)
		m.aiPrompting = false
		m.aiPrompt = ""
		if req == "" {
			return m, nil
		}
		// Keep it for up-arrow, without stacking duplicates of the same request.
		if n := len(m.aiHistory); n == 0 || m.aiHistory[n-1] != req {
			m.aiHistory = append(m.aiHistory, req)
		}
		m.aiHistIdx = len(m.aiHistory)
		h, ok := harness.ByName(m.cfg.Harness)
		if !ok || !h.Installed() {
			return m, m.setStatus("no AI harness available")
		}
		// No probe here: asking the harness a question runs nothing against the
		// repos, so there is nothing for the Repos pane to follow.
		m.takeOutputPane(outAI, m.cfg.Harness+": "+req)
		m.setBottomView(bvOutput)
		m.appendOutput("")
		m.appendAI(styleDim.Render("asking " + m.cfg.Harness + "..."))
		return m, aiAskCmd(h, m.harnessDirOrRoot(), m.aiContext(), req, m.aiRun)
	case tea.KeyRunes, tea.KeySpace:
		m.aiPrompt += string(msg.Runes)
		return m, m.scheduleGhost()
	}
	return m, nil
}

// historyStep walks the prompt history: -1 is older, +1 newer. Stepping past the
// newest entry restores whatever was being typed when browsing started, so
// up-then-down is always a round trip rather than a way to lose your sentence.
func (m *Model) historyStep(d int) tea.Cmd {
	if len(m.aiHistory) == 0 {
		return nil
	}
	if m.aiHistIdx == len(m.aiHistory) {
		m.aiDraft = m.aiPrompt // entering the history; remember the draft
	}
	i := m.aiHistIdx + d
	if i < 0 {
		i = 0
	}
	if i > len(m.aiHistory) {
		i = len(m.aiHistory)
	}
	m.aiHistIdx = i
	if i == len(m.aiHistory) {
		m.aiPrompt = m.aiDraft
	} else {
		m.aiPrompt = m.aiHistory[i]
	}
	return m.scheduleGhost()
}

// dropWord removes the last whitespace-separated word, and any spaces before it.
// handleShellPromptKey owns every key while the `!` line is open. Like
// handleAIPromptKey it switches on msg.Type — a closed set — so nothing from the
// normal keymap can leak into a half-typed command.
//
// There is deliberately no tab-completion: `:` completes against a known
// vocabulary of repo and branch names, but a shell line has no such list, and a
// tab that silently did nothing would read as broken.
func (m Model) handleShellPromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// esc leaves the shell and nothing else: a command already running keeps
		// running and keeps streaming into the pane (ctrl+c is what stops one).
		// Focus lands on Repos, because you left the shell to go do something to a
		// repo — and the Output pane you were looking at cannot be scrolled while
		// the prompt owns the keyboard anyway.
		m.shellPrompting = false
		m.shellCmd = ""
		m.focus = panelRepos
	case tea.KeyBackspace:
		if msg.Alt { // opt+backspace on macOS
			m.shellCmd = dropWord(m.shellCmd)
			break
		}
		if r := []rune(m.shellCmd); len(r) > 0 {
			m.shellCmd = string(r[:len(r)-1]) // by rune, so multi-byte survives
		}
	case tea.KeyCtrlW:
		m.shellCmd = dropWord(m.shellCmd)
	case tea.KeyCtrlU:
		m.shellCmd = ""
	case tea.KeyEnter:
		return m.runShellLine()
	case tea.KeyUp:
		m.shellHistoryStep(-1)
	case tea.KeyDown:
		m.shellHistoryStep(+1)
	case tea.KeyRunes, tea.KeySpace:
		// KeySpace is matched explicitly: bubbletea re-types a lone space out of
		// KeyRunes, so a handler that only matched KeyRunes would eat every space.
		m.shellCmd += string(msg.Runes)
	}
	return m, nil
}

// shellHistoryStep walks the `!` history: -1 older, +1 newer, both clamped (no
// wraparound). Entering the history stashes the half-typed line in shellDraft so
// walking back to the end restores it — up-then-down is always a round trip.
// Same semantics as historyStep for `:`; kept separate so the two histories
// never bleed into each other.
func (m *Model) shellHistoryStep(d int) {
	if len(m.shellHistory) == 0 {
		return
	}
	if m.shellHistIdx == len(m.shellHistory) {
		m.shellDraft = m.shellCmd
	}
	i := m.shellHistIdx + d
	if i < 0 {
		i = 0
	}
	if i > len(m.shellHistory) {
		i = len(m.shellHistory)
	}
	m.shellHistIdx = i
	if i == len(m.shellHistory) {
		m.shellCmd = m.shellDraft
		return
	}
	m.shellCmd = m.shellHistory[i]
}

// runShellLine runs what was typed at `$` in the repo the prompt was bound to.
// There is no confirm and no blocklist: `$` is an explicit shell escape, the way
// vim's `:!` is, and a y/N on every `git status` would make it useless. What it
// does instead is leave a record — the command is echoed into the pane, with the
// repo it ran in, before a single line of output arrives.
//
// The prompt STAYS OPEN afterwards, so this is a shell you sit in rather than a
// one-shot: run, read, run again. Only esc leaves.
func (m Model) runShellLine() (tea.Model, tea.Cmd) {
	line := strings.TrimSpace(m.shellCmd)
	m.shellCmd = ""
	if line == "" {
		return m, nil
	}
	if m.shellDir == "" {
		return m, m.setStatus(styleOrange.Render("no repo selected"))
	}
	if n := len(m.shellHistory); n == 0 || m.shellHistory[n-1] != line {
		m.shellHistory = append(m.shellHistory, line)
	}
	m.shellHistIdx = len(m.shellHistory)

	echo := m.shellLoc + " $ " + line
	m.takeOutputPane(outShell, echo)
	m.setBottomView(bvOutput)
	m.appendOutput(styleTitle.Render(echo))
	// A shell line can do anything to any repo, so the Repos pane follows it live
	// exactly as it follows a script.
	return m, tea.Batch(startShellCmd(line, m.shellDir, m.outputRun), m.startRepoProbe())
}

func dropWord(s string) string {
	s = strings.TrimRight(s, " \t")
	if i := strings.LastIndexAny(s, " \t"); i >= 0 {
		return s[:i+1]
	}
	return ""
}

// scheduleGhost hides the completion and re-arms it. Every keystroke bumps the
// generation, so only the tick belonging to the LAST key ever reveals anything.
func (m *Model) scheduleGhost() tea.Cmd {
	m.aiGhost = false
	m.aiGhostGen++
	return aiGhostCmd(m.aiGhostGen)
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.filtering = false
		m.filter = ""
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
	case tea.KeyRunes:
		m.filter += string(msg.Runes)
	}
	switch m.filterPanel {
	case panelScripts:
		m.scriptCursor = 0
		return m, nil
	case panelBranches:
		// Purely a view-level narrowing of the already-loaded branch list: the
		// highlighted repo doesn't change, so there is no context to reload.
		m.branchCursor = 0
		return m, nil
	case filterPRs:
		// PR filter: narrows the already-loaded PR list, nothing to reload.
		m.prCursor = 0
		return m, nil
	}
	// Each keystroke re-picks the highlighted repo, so this debounces like a
	// cursor sweep: typing "authoring" is nine repo changes, not nine reloads.
	m.cursor = 0
	return m, m.contextCmd()
}

// handleSettingsKey drives the settings/help overlay: j/k move through the
// radio-list (a theme row previews live), enter selects the highlighted row
// (editor row → inline edit), tab / shift+tab / [ / ] flip between the settings
// and keybindings faces, and ? or esc closes from either (discarding any
// un-selected theme preview).
func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editingOpenCmd {
		switch msg.Type {
		case tea.KeyEsc:
			m.editingOpenCmd = false
		case tea.KeyEnter:
			m.cfg.OpenCmd = strings.TrimSpace(m.openCmdBuf)
			m.editingOpenCmd = false
			m.saveConfig()
		case tea.KeyBackspace:
			if len(m.openCmdBuf) > 0 {
				m.openCmdBuf = m.openCmdBuf[:len(m.openCmdBuf)-1]
			}
		case tea.KeyRunes, tea.KeySpace:
			m.openCmdBuf += string(msg.Runes)
		}
		return m, nil
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "shift+tab", "[", "]":
		// Two faces, so forward and backward land in the same place. Flipping INTO
		// settings parks the cursor on the committed theme — this seeding used to
		// live in the , cases, and nothing else does it.
		m.showKeys = !m.showKeys
		if !m.showKeys {
			m.settingsCursor = themeIndex(m.cfg.Theme)
		}
	case "?":
		// ? is the door in AND out: it closes from either face, exactly like esc.
		// That owes the preview cleanup below — j/k on the settings face applies
		// themes live, so closing from there with an uncommitted preview would
		// otherwise leave it applied app-wide.
		applyTheme(themeByName(m.cfg.Theme))
		m.showHelp = false
	case "esc":
		applyTheme(themeByName(m.cfg.Theme)) // discard any live theme preview
		m.showHelp = false
	case "down", "j":
		if m.showKeys {
			// The keys face is taller than a short terminal, so j/k scrolls it.
			m.keysOffset = clampInt(m.keysOffset+1, 0, max(0, m.keysRowCount()-m.keysAvail()))
		} else {
			m.settingsCursor = clampInt(m.settingsCursor+1, 0, settingsItemCount()-1)
			m.previewSettings()
		}
	case "up", "k":
		if m.showKeys {
			m.keysOffset = clampInt(m.keysOffset-1, 0, max(0, m.keysRowCount()-m.keysAvail()))
		} else {
			m.settingsCursor = clampInt(m.settingsCursor-1, 0, settingsItemCount()-1)
			m.previewSettings()
		}
	case "enter", " ":
		if !m.showKeys {
			return m, m.settingsSelect()
		}
	}
	return m, nil
}

// previewSettings applies the theme under the cursor live (or the committed
// theme when the cursor is off the theme rows), without persisting.
func (m *Model) previewSettings() {
	if r := settingRows()[m.settingsCursor]; r.kind == skTheme {
		applyTheme(themeByName(r.val))
	} else {
		applyTheme(themeByName(m.cfg.Theme))
	}
}

// settingsSelect commits the highlighted radio row (theme / harness / news
// window / glyph, persisted) or opens the editor edit. Returns a cmd to refresh
// the news feed when the harness or news window changed. Selecting an
// uninstalled harness is a no-op.
func (m *Model) settingsSelect() tea.Cmd {
	r := settingRows()[m.settingsCursor]
	switch r.kind {
	case skTheme:
		m.cfg.Theme = r.val
		applyTheme(themeByName(r.val))
		m.saveConfig()
	case skHarness:
		if harness.Available(r.val) {
			m.cfg.Harness = r.val
			m.saveConfig()
			return m.forceRefreshNews() // a newly-picked harness re-summarizes now
		}
	case skNewsDays:
		if d, err := strconv.Atoi(r.val); err == nil {
			m.cfg.NewsDays = d
			m.saveConfig()
			return m.forceRefreshNews() // apply the new window immediately
		}
	case skMaxDepth:
		// Deliberately does NOT set cfg here: the walk has to find something
		// first. rescanMsg commits the depth only on a non-empty result, so
		// picking a depth with no repos under it leaves you where you were
		// rather than staring at an empty pane.
		if d, err := strconv.Atoi(r.val); err == nil && d != m.cfg.MaxDepth {
			return m.rescanCmd(d)
		}
	case skGlyph:
		m.cfg.StatusGlyphs = r.val
		m.saveConfig()
	case skMouse:
		// Takes effect now, not on the next launch: the terminal is told to start
		// or stop reporting the mouse, so drag-to-select comes back the moment you
		// pick off.
		m.cfg.Mouse = r.val
		m.saveConfig()
		if m.cfg.MouseEnabled() {
			return tea.EnableMouseCellMotion
		}
		return tea.DisableMouse
	case skEditor:
		m.editingOpenCmd = true
		m.openCmdBuf = m.cfg.OpenCmd
	}
	return nil
}

// saveConfig persists the current config (best-effort; a write failure leaves
// the change applied for this session).
func (m Model) saveConfig() {
	_ = config.Save(m.cfg, "")
}

func baseName(p string) string { return filepath.Base(p) }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// setTopView focuses the top slot and shows v. Number keys, [/], right and enter
// all route through here so no entrance skips another's side effects.
func (m *Model) setTopView(v topView) {
	m.focus = panelBranches
	if v != tvPRs {
		m.clearPRFilter() // leaving the PRs sub-view drops its `/` filter
	}
	m.topView = v
	if v == tvPRs {
		m.autoPickPRList()
	}
}

// autoPickPRList points the PRs pane at whichever list has something in it: your
// own PRs normally, review requests when yours are empty and reviews are
// waiting. Opening onto an empty list while nine PRs sit in the other one wastes
// the keypress — you read "No open PRs authored by you", press `m`, and only
// then see the work. With nothing in either list it stays on your own, so the
// pane says the more useful of the two empty messages.
//
// It runs both when the pane opens and when the lists land, because they load
// async: `4` is normally pressed while both are still empty, so picking only at
// open time would never fire in the case that matters. Once the user has chosen
// with `m` it stops entirely — an explicit choice has to outlive an `r` refresh
// that would otherwise pull the pane out from under them.
func (m *Model) autoPickPRList() {
	if m.prChosen {
		return
	}
	if want := len(m.prMine) == 0 && len(m.prReview) > 0; want != m.prShowReview {
		m.prShowReview = want
		m.prCursor = 0
	}
}

// setBottomView focuses the bottom slot and shows v, with the same side effects:
// a PR needle is meaningless down here, and Changes has to (re)load its files.
func (m *Model) setBottomView(v bottomView) tea.Cmd {
	m.focus = panelBottom
	m.clearPRFilter()
	m.bottomView = v
	if v == bvChanges {
		m.changeShowDiff = false
		return m.loadChangesCmd()
	}
	return nil
}

// cycleTab moves the focused pane's tab bar by delta, wrapping — a no-op in Repos
// and Scripts, which have no tabs. On its own keys so `tab` stays pane-cycling
// everywhere. The (x%n + n) % n keeps the modulo positive wrapping backwards.
func (m *Model) cycleTab(delta int) tea.Cmd {
	switch m.focus {
	case panelBranches:
		n := int(topViewCount)
		m.setTopView(topView(((int(m.topView)+delta)%n + n) % n))
	case panelBottom:
		n := int(bottomViewCount)
		return m.setBottomView(bottomView(((int(m.bottomView)+delta)%n + n) % n))
	}
	return nil
}

// topScroll moves the cursor of the active top-right view (Branches or PRs).
func (m *Model) topScroll(delta int) {
	if m.topView == tvPRs {
		m.prCursor = clampInt(m.prCursor+delta, 0, max(0, len(m.visiblePRs())-1))
		return
	}
	m.branchCursor = clampInt(m.branchCursor+delta, 0, max(0, len(m.visibleBranches())-1))
}

// bottomScroll moves the cursor/scroll of the active bottom view by delta.
func (m *Model) bottomScroll(delta int) {
	switch m.bottomView {
	case bvGraph:
		m.graphSel = clampInt(m.graphSel+delta, 0, len(m.graphCommits)) // 0 == WIP
	case bvChanges:
		if m.changeShowDiff {
			m.changeDiffOff = clampInt(m.changeDiffOff+delta, 0, max(0, len(m.changeDiff)-1))
		} else {
			m.changeCursor = clampInt(m.changeCursor+delta, 0, max(0, len(m.changeFiles)-1))
		}
	case bvOutput:
		m.outputOffset = clampInt(m.outputOffset+delta, 0, max(0, len(m.outputLines)-1))
	}
}

// clearBranchFilter drops an active `/` filter *only* when it's scoped to the
// Branches panel. A branch filter belongs to one repo's branch list, so it must
// be cleared when the selected repo changes — otherwise the stale needle silently
// filters the next repo's branches. A Repos/Scripts filter is left untouched, so
// navigating within a committed repo filter still works.
func (m *Model) clearBranchFilter() {
	if m.filterPanel == panelBranches {
		m.filter = ""
		m.filterPanel = panelRepos
		m.branchCursor = 0
	}
}

// clearPRFilter drops a `/` filter scoped to the PRs sub-view (filterPanel ==
// filterPRs). Called when the top slot switches to Branches or focus moves to the
// bottom slot, where the PR needle is meaningless and would otherwise persist.
func (m *Model) clearPRFilter() {
	if m.filterPanel == filterPRs {
		m.filter = ""
		m.filterPanel = panelRepos
		m.prCursor = 0
	}
}

// noLocalClone is the one message for "this PR's repo isn't among the repos
// manygit scanned". enter and o fail for exactly that reason, so they say
// exactly this, differing only in the verb — two wordings for one condition
// would teach the reader they are different problems. It names the tree rather
// than the pane, because that is what has to change to fix it: clone the repo
// under the root, or point manygit at a root that has it.
func noLocalClone(slug, verb string) string {
	return slug + " isn't in this tree — no local clone to " + verb
}

// openTarget resolves what `o` should open. In the PRs pane that is the
// highlighted PR's local clone, not whatever the Repos cursor happens to be on:
// you act on what you are looking at. Everywhere else it is the cursor repo.
//
// Returns (path, "") when there is something to open, ("", slug) when the
// highlighted PR has no local clone — so the caller can name the missing repo —
// and ("", "") when there is nothing under the cursor at all.
func (m Model) openTarget() (path, missingSlug string) {
	if m.focus == panelBranches && m.topView == tvPRs {
		prs := m.visiblePRs()
		if m.prCursor < 0 || m.prCursor >= len(prs) {
			return "", ""
		}
		pr := prs[m.prCursor]
		if r := m.repoBySlug(pr.RepoSlug); r != nil {
			return r.repo.Path, ""
		}
		return "", pr.RepoSlug
	}
	if r := m.currentVisible(m.visibleRepos()); r != nil {
		return r.repo.Path, ""
	}
	return "", ""
}

// repoBySlug finds the discovered repo whose origin remote is slug (case-
// insensitive), or nil. Uses the slug computed at status-load time, so it does no
// git exec on the keystroke.
func (m Model) repoBySlug(slug string) *repoVM {
	if slug == "" {
		return nil
	}
	want := strings.ToLower(slug)
	for _, r := range m.repos {
		if r.status.Slug != "" && strings.ToLower(r.status.Slug) == want {
			return r
		}
	}
	return nil
}

// checkoutPR checks out the highlighted PR into its matching local clone: it maps
// the PR's repo slug to a discovered repo by origin, then runs `gh pr checkout`.
// Sets an explanatory status (and returns just that) when there's no local clone
// or the tree is dirty.
func (m *Model) checkoutPR() tea.Cmd {
	prs := m.visiblePRs()
	if m.prCursor < 0 || m.prCursor >= len(prs) {
		return nil
	}
	pr := prs[m.prCursor]
	r := m.repoBySlug(pr.RepoSlug)
	if r == nil {
		return m.setStatus(styleOrange.Render(noLocalClone(pr.RepoSlug, "check out")))
	}
	if r.status.DirtyCount > 0 {
		return m.setStatus(styleOrange.Render("checkout skipped: dirty working tree in " + baseName(r.repo.Path)))
	}
	num := strconv.Itoa(pr.Number)
	return tea.Batch(
		m.setStatus(styleDim.Render("checking out PR #"+num+" in "+baseName(r.repo.Path)+"...")),
		ghCheckoutCmd(m.sem, r.repo.Path, pr.Number),
	)
}

// keepCursorOn re-points the cursor at path within the current visible set, or
// clamps to the top when it's gone. It preserves the filter — this is for
// changes that reshuffle the filtered list, not escape it. Reloads the panes
// only when the cursor lands on a different repo (a needless reload collapses an
// open diff by resetting graphSel/graphOffset/changeShowDiff).

// reclampCursor keeps the cursor on a real visible row after a status change and
// reports whether the panels need reloading. A statusMsg can drop the highlighted
// row from a filtered list (`F` hides a repo once it's clean; a branch `/needle`
// stops matching after a checkout), leaving the index dangling past the end or
// silently addressing a different repo than the panels show. `was` is the path it
// was on; reloads only if it ends up elsewhere.
func (m *Model) reclampCursor(was string) tea.Cmd {
	vis := m.visibleRepos()
	if m.cursor >= len(vis) {
		m.cursor = len(vis) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if cur := m.currentVisible(vis); cur != nil && cur.repo.Path != was {
		return m.loadContextCmd()
	}
	return nil
}

func (m *Model) keepCursorOn(path string) tea.Cmd {
	m.cursor = 0
	for i, r := range m.visibleRepos() {
		if r.repo.Path == path {
			m.cursor = i
			break
		}
	}
	if r := m.currentVisible(m.visibleRepos()); r != nil && r.repo.Path == path {
		return nil // same repo still under the cursor — nothing to reload
	}
	return m.loadContextCmd()
}

// repoProbeInterval is how often, while a script runs, manygit checks whether
// any repo moved. The check is ~11 stat calls per repo and spawns no processes
// (measured at ~0.5ms across 28 repos), so it can be frequent; the Status() reads
// it triggers are the expensive part — 6-8 git subprocesses each — and only run
// for repos that actually changed.
const repoProbeInterval = 1200 * time.Millisecond

// repoPaths is the paths of every discovered repo, snapshotted so the probe
// goroutine never touches the model.
func (m Model) repoPaths() []string {
	paths := make([]string, 0, len(m.repos))
	for _, r := range m.repos {
		paths = append(paths, r.repo.Path)
	}
	return paths
}

// takeOutputPane hands the Output pane to a new producer, superseding whatever
// was writing there — a streaming script, a pending AI reply, or both.
//
// There is one pane but two producers with their own generation counters: the
// script runner stamps scriptOutMsg with outputRun, the AI harness stamps its
// replies with aiRun. Bumping only your own leaves the other one's messages
// still passing their staleness check, so a script that is superseded by an AI
// request keeps appending its lines into the AI's pane, and an AI reply that
// arrives after a script started wipes the script's output and clears
// outputRunning. Bumping both is what makes the handover exclusive.
//
// It also drops the repo probe: the run that chain belonged to no longer owns
// the pane, and its next tick would be discarded on the run guard anyway.
// Producers that actually change repos re-arm with startRepoProbe.
func (m *Model) takeOutputPane(kind outputKind, title string) {
	m.killRunning() // stop what we're superseding rather than orphaning it
	m.outputRun++
	m.aiRun++
	m.outputKind = kind
	m.outputTitle = title
	m.outputLines = nil
	m.outputOffset = 0
	m.outputRunning = true
	m.shellKilled = false
	m.probing = false
}

// cancelledText names what a cancel just stopped. Scripts became cancellable in
// the same change that added `!`, so this cannot assume the run was a shell one —
// shellLoc is meaningless (or stale from an earlier `!`) for a script run.
func (m Model) cancelledText() string {
	if m.outputKind == outShell {
		return m.shellLoc + ": cancelled"
	}
	return "cancelled " + m.outputTitle
}

// killRunning stops the command currently streaming into the Output pane, if
// there is one. Safe to call at any time: cancel() is idempotent and harmless
// after the process has already exited.
func (m *Model) killRunning() {
	if m.shellCancel == nil {
		return
	}
	m.shellKilled = true
	m.shellCancel()
	m.shellCancel = nil
}

// startRepoProbe baselines every repo and arms the probe for the current
// outputRun, returning nil if a chain is already live FOR THAT RUN. The baseline
// is taken when the script STARTS, not on the first tick: whatever the script
// changed in the meantime would otherwise be baked into the baseline and never
// reported. It costs ~11 stat calls per repo (~0.5ms across 28) on a keystroke
// that is already spawning a shell.
//
// The guard is tagged by run, and both halves matter:
//
//   - Same run, twice: refuse. Every tick re-arms itself, so a second chain for
//     one run would never end and would double the stat rate forever. This is
//     reachable because the AI-harness paths set outputRunning without bumping
//     outputRun, so outputRunning alone wouldn't prevent it.
//   - New run: arm, even though probing is still true. Starting a script while
//     one is streaming is unguarded and runSelectedScript bumps outputRun to
//     supersede the old one — which means the in-flight tick now carries a stale
//     run and is dropped without re-arming. If the new run didn't arm here,
//     nothing would be ticking and the second script would get no live updates
//     at all.
func (m *Model) startRepoProbe() tea.Cmd {
	if m.probing && m.probeRun == m.outputRun {
		return nil
	}
	for _, r := range m.repos {
		r.fp = git.Fingerprint(r.repo.Path)
	}
	m.probing, m.probeRun = true, m.outputRun
	return repoProbeCmd(m.outputRun, m.repoPaths())
}

// repoProbeCmd samples every repo's fingerprint after repoProbeInterval. The
// sampling happens inside the command, off the render loop.
func repoProbeCmd(run int, paths []string) tea.Cmd {
	return tea.Tick(repoProbeInterval, func(time.Time) tea.Msg {
		fps := make(map[string]int64, len(paths))
		for _, p := range paths {
			fps[p] = git.Fingerprint(p)
		}
		return repoProbeMsg{run: run, fps: fps}
	})
}

// probeChanged adopts a fresh fingerprint sample and returns the paths that
// moved since the last one. A repo with no baseline yet (fp == 0: added by a
// rescan mid-script) adopts the sample silently — reporting it would fire a
// Status() for every repo on the first tick, which is the sweep this avoids.
func (m *Model) probeChanged(fps map[string]int64) []string {
	var changed []string
	for _, r := range m.repos {
		fp, ok := fps[r.repo.Path]
		if !ok || fp == r.fp {
			continue
		}
		if r.fp != 0 {
			changed = append(changed, r.repo.Path)
		}
		r.fp = fp
	}
	return changed
}

// restatAllCmd re-reads every repo's local status. No network: this is the same
// work Init does at launch (~170ms of concurrent git across 28 repos), not the
// `r` refresh, so finishing a script can't spray fetches at every remote.
func (m Model) restatAllCmd() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.repos))
	for _, r := range m.repos {
		cmds = append(cmds, statusCmd(r.repo.Path))
	}
	return tea.Batch(cmds...)
}

// loadContextIfCurrent reloads the Branches/Graph/Changes panes only when path
// is the repo those panes are actually showing. Something changing in a repo the
// cursor isn't on leaves them correct, so reloading would spend 2-3 git
// subprocesses redrawing identical content — and results are path-guarded in
// Update anyway, so the work would be discarded on arrival.
func (m Model) loadContextIfCurrent(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	if r := m.currentVisible(m.visibleRepos()); r != nil && r.repo.Path == path {
		return m.loadContextCmd()
	}
	return nil
}

// runSelectedScript starts the highlighted script in the background and flips the
// bottom slot to Output (7) so its live output is visible; nil when the Scripts
// cursor is out of range.
func (m *Model) runSelectedScript() tea.Cmd {
	vs := m.visibleScripts()
	if m.scriptCursor < 0 || m.scriptCursor >= len(vs) {
		return nil
	}
	m.takeOutputPane(outScript, vs[m.scriptCursor].Name) // supersede any previous producer
	m.focus = panelBottom
	m.bottomView = bvOutput
	// Arm the probe so the Repos pane follows what the script does as it goes,
	// instead of waiting for the end.
	return tea.Batch(m.runScriptCmd(), m.startRepoProbe())
}

// checkoutSelected checks out the highlighted branch when the Branches panel is
// focused; nil (with an optional status set) otherwise.
func (m *Model) checkoutSelected(vis []*repoVM) tea.Cmd {
	branches := m.visibleBranches()
	r := m.currentVisible(vis)
	if r == nil || m.focus != panelBranches || m.branchCursor >= len(branches) {
		return nil
	}
	if r.status.DirtyCount > 0 {
		return m.setStatus(styleOrange.Render("checkout skipped: dirty working tree"))
	}
	return checkoutCmd(m.sem, r.repo.Path, branches[m.branchCursor].LocalName())
}

// targets returns the repo actions apply to: the highlighted (cursor) repo.
func (m Model) targets() []*repoVM {
	if r := m.currentVisible(m.visibleRepos()); r != nil {
		return []*repoVM{r}
	}
	return nil
}

const statusTTL = 4 * time.Second

// setStatus sets the status line and returns a command that clears it after
// statusTTL — unless a newer status replaces it first (guarded by statusGen).
func (m *Model) setStatus(s string) tea.Cmd {
	m.statusLine = s
	m.statusGen++
	gen := m.statusGen
	return tea.Tick(statusTTL, func(time.Time) tea.Msg { return statusExpireMsg{gen: gen} })
}

func (m Model) syncResultText(msg syncDoneMsg) string {
	name := baseName(msg.path)
	switch {
	case msg.skipped:
		return styleOrange.Render("sync " + name + " skipped: " + msg.reason)
	case msg.err != nil:
		return styleRed.Render("sync " + name + " failed: " + msg.err.Error())
	default:
		return styleGreen.Render("synced " + name)
	}
}
