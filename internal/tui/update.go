package tui

import (
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
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
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
		}
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
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
	return m, nil
}
