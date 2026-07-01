package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/config"
	"manygit/internal/discover"
	"manygit/internal/git"
)

type panel int

const (
	panelRepos panel = iota
	panelBranches
	panelLog
)

// repoVM is the per-repo view model.
type repoVM struct {
	repo     discover.Repo
	status   git.RepoStatus
	loaded   bool
	fetching bool
}

// Model is the Bubble Tea model.
type Model struct {
	cfg   config.Config
	repos []*repoVM

	cursor int
	focus  panel

	selected     map[string]bool
	filter       string
	filtering    bool
	branches     []git.Branch
	branchCursor int
	log          []string
	statusLine   string

	sem           chan struct{}
	width, height int
}

// New builds a Model from discovered repos.
func New(cfg config.Config, repos []discover.Repo) Model {
	vms := make([]*repoVM, len(repos))
	for i, r := range repos {
		vms[i] = &repoVM{repo: r}
	}
	conc := cfg.Concurrency
	if conc < 1 {
		conc = 1
	}
	return Model{
		cfg:      cfg,
		repos:    vms,
		focus:    panelRepos,
		selected: map[string]bool{},
		sem:      make(chan struct{}, conc),
	}
}

// Init loads local status for every repo (fast, ungated). Background fetch is
// added in Task 9; context load in Task 10.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range m.repos {
		cmds = append(cmds, statusCmd(r.repo.Path))
	}
	return tea.Batch(cmds...)
}
