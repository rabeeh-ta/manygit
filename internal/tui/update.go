package tui

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/config"
	"manygit/internal/harness"
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
		for _, r := range m.repos {
			if r.repo.Path == msg.path {
				r.status = msg.st
				r.loaded = true
				break
			}
		}
		return m, nil
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
		m.focusRepoByPath(msg.path) // land on that repo's branches, ready to review
		return m, tea.Batch(exp, statusCmd(msg.path), m.loadContextCmd())
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
	case scriptOutMsg:
		stale := msg.run != m.outputRun // a superseded run (user started another script)
		if msg.done {
			if stale {
				return m, nil // superseded run finished draining; drop it silently
			}
			m.outputRunning = false
			var s string
			if msg.err != nil {
				s = styleRed.Render("script " + m.outputTitle + " failed: " + msg.err.Error())
			} else {
				s = styleGreen.Render("ran " + m.outputTitle)
			}
			return m, m.setStatus(s)
		}
		if !stale {
			m.appendOutput(msg.line)
		}
		// Keep reading even a superseded run so its process drains and exits.
		return m, readScriptLine(msg.scanner, msg.run)
	case statusExpireMsg:
		if msg.gen == m.statusGen {
			m.statusLine = ""
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// loadTagsCmd loads the latest tag for every repo (fast local reads, ungated).
func (m Model) loadTagsCmd() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.repos))
	for _, r := range m.repos {
		cmds = append(cmds, latestTagCmd(r.repo.Path))
	}
	return tea.Batch(cmds...)
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

// appendOutput adds a line to the Output view, keeping the view pinned to the
// tail (auto-follow) unless the user has scrolled up.
func (m *Model) appendOutput(line string) {
	atBottom := m.outputOffset >= len(m.outputLines)-1
	m.outputLines = append(m.outputLines, line)
	if atBottom {
		m.outputOffset = len(m.outputLines) - 1
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	if m.showHelp {
		return m.handleSettingsKey(msg)
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
			if m.newsOffset < len(m.newsFeed)-1 {
				m.newsOffset++
			}
		case "up", "k":
			if m.newsOffset > 0 {
				m.newsOffset--
			}
		}
		return m, nil
	}
	vis := m.visibleRepos()
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.showHelp = true
		m.showKeys = false
		m.settingsCursor = themeIndex(m.cfg.Theme) // start on the active theme
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
		m.showTagsInline = !m.showTagsInline
		if m.showTagsInline {
			return m, m.loadTagsCmd()
		}
	case "1":
		m.focus = panelRepos
	case "2":
		m.focus = panelScripts
	case "3":
		m.focus = panelBranches
		m.clearPRFilter() // leaving the PRs sub-view drops its `/` filter
		m.topView = tvBranches
	case "4":
		m.focus = panelBranches
		m.topView = tvPRs
	case "5":
		m.focus = panelBottom
		m.clearPRFilter()
		m.bottomView = bvGraph
	case "6":
		m.focus = panelBottom
		m.clearPRFilter()
		m.bottomView = bvChanges
		m.changeShowDiff = false
		return m, m.loadChangesCmd()
	case "7":
		m.focus = panelBottom
		m.clearPRFilter()
		m.bottomView = bvOutput
	case "tab":
		m.focus = (m.focus + 1) % panelCount
	case "right":
		// →/← hop between the two panels you actually browse together: Repos and
		// the highlighted repo's Branches. Deliberately scoped to those two — from
		// any other panel the arrows stay unbound, leaving them free for
		// panel-local uses (e.g. scrolling a wide diff) later.
		if m.focus == panelRepos {
			m.focus = panelBranches
			m.topView = tvBranches // → always shows the highlighted repo's branches
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
				return m, m.loadContextCmd()
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
				return m, m.loadContextCmd()
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
			m.focus = panelBranches
			m.topView = tvBranches
			m.clearPRFilter()
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
			m.prCursor = 0
		}
	case "esc":
		// Back-navigate the bottom slot: diff → file list → graph.
		if m.focus == panelBottom && m.bottomView == bvChanges {
			if m.changeShowDiff {
				m.changeShowDiff = false
			} else {
				m.bottomView = bvGraph
			}
		}
	case "o":
		if r := m.currentVisible(vis); r != nil {
			path, openCmd := r.repo.Path, m.cfg.OpenCmd
			return m, func() tea.Msg {
				_ = exec.Command(openCmd, path).Start() // detached; ignore result
				return nil
			}
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
	m.cursor = 0
	return m, m.loadContextCmd()
}

// handleSettingsKey drives the settings/help overlay: j/k move through the
// radio-list (a theme row previews live), enter selects the highlighted row
// (editor row → inline edit), tab/? flips to the keybindings reference, esc
// closes (discarding any un-selected theme preview).
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
	case "tab", "?":
		m.showKeys = !m.showKeys
	case "esc":
		applyTheme(themeByName(m.cfg.Theme)) // discard any live theme preview
		m.showHelp = false
	case "down", "j":
		if !m.showKeys {
			m.settingsCursor = clampInt(m.settingsCursor+1, 0, settingsItemCount()-1)
			m.previewSettings()
		}
	case "up", "k":
		if !m.showKeys {
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
	case skGlyph:
		m.cfg.StatusGlyphs = r.val
		m.saveConfig()
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
		return m.setStatus(styleOrange.Render("PR repo " + pr.RepoSlug + " is not in view"))
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

// focusRepoByPath moves the repo cursor to the repo at path and focuses its
// Branches pane, so a PR checkout lands you ready to review. It clears any
// repo/branch filter and the attention view so the target is visible and the
// cursor indexes m.repos directly. No-op if path isn't among the repos.
func (m *Model) focusRepoByPath(path string) {
	idx := -1
	for i, r := range m.repos {
		if r.repo.Path == path {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	m.filter = ""
	m.filterPanel = panelRepos
	m.filterAttention = false
	m.cursor = idx
	m.branchCursor = 0
	m.focus = panelBranches
	m.topView = tvBranches // show the checked-out repo's branches, not the PR list
}

// runSelectedScript starts the highlighted script in the background and flips the
// bottom slot to Output (7) so its live output is visible; nil when the Scripts
// cursor is out of range.
func (m *Model) runSelectedScript() tea.Cmd {
	vs := m.visibleScripts()
	if m.scriptCursor < 0 || m.scriptCursor >= len(vs) {
		return nil
	}
	m.outputRun++ // supersede any still-streaming previous run
	m.outputTitle = vs[m.scriptCursor].Name
	m.outputLines = nil
	m.outputOffset = 0
	m.outputRunning = true
	m.focus = panelBottom
	m.bottomView = bvOutput
	return m.runScriptCmd()
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
