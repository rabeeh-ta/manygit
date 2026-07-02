package tui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
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
		var cmd tea.Cmd
		for _, r := range m.repos {
			if r.repo.Path == msg.path {
				r.fetching = false
				if msg.err == nil {
					cmd = statusCmd(msg.path) // refresh ahead/behind asynchronously
				}
				break
			}
		}
		return m, cmd
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
		if msg.err != nil {
			s = styleRed.Render("push " + name + " failed: " + msg.err.Error())
		} else {
			s = styleGreen.Render("pushed " + name)
		}
		exp := m.setStatus(s)
		return m, tea.Batch(exp, statusCmd(msg.path))
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
			if m.branchCursor >= len(m.branches) {
				m.branchCursor = 0
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

// loadContextCmd loads branches + the commit graph for the highlighted repo.
func (m Model) loadContextCmd() tea.Cmd {
	r := m.currentVisible(m.visibleRepos())
	if r == nil {
		return nil
	}
	return tea.Batch(branchesCmd(r.repo.Path), graphCmd(r.repo.Path, 200))
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
		if s := msg.String(); s == "q" || s == "ctrl+c" {
			return m, tea.Quit
		}
		m.showHelp = false // any other key dismisses help
		return m, nil
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
	vis := m.visibleRepos()
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.showHelp = true
	case "g":
		// Full-screen colored commit graph (reuses the loaded graph).
		m.showGraph = true
		m.graphOffset = 0
	case "1":
		m.focus = panelRepos
	case "2":
		m.focus = panelScripts
	case "3":
		m.focus = panelBranches
	case "4":
		m.focus = panelBottom
		m.bottomView = bvGraph
	case "5":
		m.focus = panelBottom
		m.bottomView = bvChanges
		m.changeShowDiff = false
		return m, m.loadChangesCmd()
	case "6":
		m.focus = panelBottom
		m.bottomView = bvOutput
	case "tab":
		m.focus = (m.focus + 1) % panelCount
	case "down", "j":
		// Navigate within the FOCUSED panel (repos vs. branches), so browsing
		// branches doesn't move the repo cursor and reload the panels.
		switch m.focus {
		case panelRepos:
			if m.cursor < len(vis)-1 {
				m.cursor++
				return m, m.loadContextCmd()
			}
		case panelBranches:
			if m.branchCursor < len(m.branches)-1 {
				m.branchCursor++
			}
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
				return m, m.loadContextCmd()
			}
		case panelBranches:
			if m.branchCursor > 0 {
				m.branchCursor--
			}
		case panelScripts:
			if m.scriptCursor > 0 {
				m.scriptCursor--
			}
		case panelBottom:
			m.bottomScroll(-1)
		}
	case "J":
		if m.focus == panelBranches && m.branchCursor < len(m.branches)-1 {
			m.branchCursor++
		}
	case "K":
		if m.focus == panelBranches && m.branchCursor > 0 {
			m.branchCursor--
		}
	case "enter":
		// In the Changes view, enter opens the selected file's diff in-place.
		if m.focus == panelBottom && m.bottomView == bvChanges && !m.changeShowDiff {
			if r := m.currentVisible(vis); r != nil && m.changeCursor < len(m.changeFiles) {
				return m, diffCmd(r.repo.Path, m.selectedRef(), m.changeFiles[m.changeCursor].Path)
			}
			return m, nil
		}
		cmd := m.checkoutSelected(vis)
		return m, cmd
	case "b":
		cmd := m.checkoutSelected(vis)
		return m, cmd
	case "esc":
		// Close the in-place diff, back to the file list.
		if m.focus == panelBottom && m.bottomView == bvChanges && m.changeShowDiff {
			m.changeShowDiff = false
		}
	case "o":
		if r := m.currentVisible(vis); r != nil {
			path, openCmd := r.repo.Path, m.cfg.OpenCmd
			return m, func() tea.Msg {
				_ = exec.Command(openCmd, path).Start() // detached; ignore result
				return nil
			}
		}
	case " ":
		switch m.focus {
		case panelScripts:
			vs := m.visibleScripts()
			if m.scriptCursor < 0 || m.scriptCursor >= len(vs) {
				return m, nil
			}
			// Run in the background and surface its live output in view 6.
			m.outputRun++ // supersede any still-streaming previous run
			m.outputTitle = vs[m.scriptCursor].Name
			m.outputLines = nil
			m.outputOffset = 0
			m.outputRunning = true
			m.focus = panelBottom
			m.bottomView = bvOutput
			return m, m.runScriptCmd()
		case panelRepos:
			// Jump into the highlighted repo's branches.
			m.focus = panelBranches
			m.branchCursor = 0
		default:
			m.focus = panelRepos
		}
	case "F":
		// Toggle the "needs attention" view: only repos with changes / ahead / behind.
		m.filterAttention = !m.filterAttention
		m.cursor = 0
		return m, m.loadContextCmd()
	case "/":
		m.filtering = true
		m.filter = ""
		if m.focus == panelScripts {
			m.filterPanel = panelScripts
			m.scriptCursor = 0
		} else {
			m.filterPanel = panelRepos
			m.cursor = 0
		}
	case "f":
		if r := m.currentVisible(vis); r != nil && !r.fetching {
			r.fetching = true
			return m, fetchCmd(m.sem, r.repo.Path)
		}
	case "r":
		var cmds []tea.Cmd
		for _, r := range m.repos {
			if r.fetching {
				continue
			}
			r.fetching = true
			cmds = append(cmds, fetchCmd(m.sem, r.repo.Path))
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
			cmds = append(cmds, pushCmd(m.sem, r.repo.Path))
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
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
	if m.filterPanel == panelScripts {
		m.scriptCursor = 0
		return m, nil
	}
	m.cursor = 0
	return m, m.loadContextCmd()
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

// checkoutSelected checks out the highlighted branch when the Branches panel is
// focused; nil (with an optional status set) otherwise.
func (m *Model) checkoutSelected(vis []*repoVM) tea.Cmd {
	r := m.currentVisible(vis)
	if r == nil || m.focus != panelBranches || m.branchCursor >= len(m.branches) {
		return nil
	}
	if r.status.DirtyCount > 0 {
		return m.setStatus(styleOrange.Render("checkout skipped: dirty working tree"))
	}
	target := m.branches[m.branchCursor]
	name := target.Name
	if target.IsRemote {
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
	}
	return checkoutCmd(m.sem, r.repo.Path, name)
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
