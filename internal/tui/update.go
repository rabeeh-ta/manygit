package tui

import (
	"os/exec"
	"path/filepath"
	"strings"

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
		m = m.applySyncResult(msg)
		if !msg.skipped && msg.err == nil {
			return m, statusCmd(msg.path) // refresh status asynchronously after a successful sync
		}
		return m, nil
	case pushDoneMsg:
		name := baseName(msg.path)
		if msg.err != nil {
			m.statusLine = styleRed.Render("push " + name + " failed: " + msg.err.Error())
		} else {
			m.statusLine = styleGreen.Render("pushed " + name)
		}
		return m, statusCmd(msg.path)
	case checkoutDoneMsg:
		name := baseName(msg.path)
		if msg.err != nil {
			m.statusLine = styleRed.Render("checkout " + name + " failed: " + msg.err.Error())
			return m, nil
		}
		m.statusLine = styleGreen.Render("checked out " + msg.branch + " in " + name)
		return m, tea.Batch(statusCmd(msg.path), m.loadContextCmd())
	case branchesMsg:
		if r := m.currentVisible(m.visibleRepos()); r != nil && r.repo.Path == msg.path {
			m.branches = msg.branches
			if m.branchCursor >= len(m.branches) {
				m.branchCursor = 0
			}
		}
		return m, nil
	case logMsg:
		if r := m.currentVisible(m.visibleRepos()); r != nil && r.repo.Path == msg.path {
			m.log = msg.lines
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// loadContextCmd loads branches + log for the highlighted repo.
func (m Model) loadContextCmd() tea.Cmd {
	r := m.currentVisible(m.visibleRepos())
	if r == nil {
		return nil
	}
	return tea.Batch(branchesCmd(r.repo.Path), logCmd(r.repo.Path, 50))
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	vis := m.visibleRepos()
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "1":
		m.focus = panelRepos
	case "2":
		m.focus = panelBranches
	case "3":
		m.focus = panelLog
	case "tab":
		m.focus = (m.focus + 1) % 3
	case "down", "j":
		if m.cursor < len(vis)-1 {
			m.cursor++
			return m, m.loadContextCmd()
		}
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			return m, m.loadContextCmd()
		}
	case "J":
		if m.focus == panelBranches && m.branchCursor < len(m.branches)-1 {
			m.branchCursor++
		}
	case "K":
		if m.focus == panelBranches && m.branchCursor > 0 {
			m.branchCursor--
		}
	case "b", "enter":
		if r := m.currentVisible(vis); r != nil && m.focus == panelBranches && m.branchCursor < len(m.branches) {
			if r.status.DirtyCount > 0 {
				m.statusLine = styleOrange.Render("checkout skipped: dirty working tree")
				return m, nil
			}
			target := m.branches[m.branchCursor]
			name := target.Name
			if target.IsRemote {
				if idx := strings.LastIndex(name, "/"); idx >= 0 {
					name = name[idx+1:]
				}
			}
			return m, checkoutCmd(m.sem, r.repo.Path, name)
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
		if r := m.currentVisible(vis); r != nil {
			m.selected[r.repo.Path] = !m.selected[r.repo.Path]
		}
	case "a":
		allSel := len(vis) > 0
		for _, r := range vis {
			if !m.selected[r.repo.Path] {
				allSel = false
				break
			}
		}
		for _, r := range vis {
			m.selected[r.repo.Path] = !allSel
		}
	case "/":
		m.filtering = true
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
		m.cursor = 0
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
		m.cursor = 0
	case tea.KeyRunes:
		m.filter += string(msg.Runes)
		m.cursor = 0
	}
	return m, m.loadContextCmd()
}

func baseName(p string) string { return filepath.Base(p) }

// targets returns the action targets: the selection if any, else the highlighted repo.
func (m Model) targets() []*repoVM {
	var sel []*repoVM
	for _, r := range m.repos {
		if m.selected[r.repo.Path] {
			sel = append(sel, r)
		}
	}
	if len(sel) > 0 {
		return sel
	}
	if r := m.currentVisible(m.visibleRepos()); r != nil {
		return []*repoVM{r}
	}
	return nil
}

func (m Model) applySyncResult(msg syncDoneMsg) Model {
	name := baseName(msg.path)
	switch {
	case msg.skipped:
		m.statusLine = styleOrange.Render("sync " + name + " skipped: " + msg.reason)
	case msg.err != nil:
		m.statusLine = styleRed.Render("sync " + name + " failed: " + msg.err.Error())
	default:
		m.statusLine = styleGreen.Render("synced " + name)
	}
	return m
}
