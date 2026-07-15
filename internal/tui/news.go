package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/git"
	"manygit/internal/harness"
)

const (
	newsRotate       = 12 * time.Second // top-bar headline dwell time (slow enough to read)
	newsMaxHeadlines = 10               // hard cap on headlines, in case the harness overshoots
)

// newsRepo is the minimal per-repo info the news refresh needs (captured up
// front so the background command doesn't touch the live Model).
type newsRepo struct {
	name, path string
}

func (m Model) newsRepos() []newsRepo {
	rs := make([]newsRepo, 0, len(m.repos))
	for _, r := range m.repos {
		rs = append(rs, newsRepo{name: r.repo.Name, path: r.repo.Path})
	}
	return rs
}

// newsRefreshCmd gathers recent commits across the repos and asks the harness to
// summarize them into short news-feed headlines. gen tags the result so a stale
// refresh is dropped.
func newsRefreshCmd(h harness.Harness, dir string, repos []newsRepo, days, gen int) tea.Cmd {
	since := ""
	if days > 0 {
		since = fmt.Sprintf("%d days ago", days)
	}
	return func() tea.Msg {
		var b strings.Builder
		any := false
		for _, r := range repos {
			ref := git.MainRef(r.path)                             // main/master only, not every branch
			commits, _ := git.RecentCommits(r.path, ref, 0, since) // all in the time window
			if len(commits) == 0 {
				continue
			}
			any = true
			fmt.Fprintf(&b, "repo %s (%s):\n", r.name, ref)
			for _, c := range commits {
				fmt.Fprintf(&b, "  - %s\n", c)
			}
		}
		if !any {
			return newsFeedMsg{gen: gen}
		}
		prompt := fmt.Sprintf(`Below are recent commits on the main branch of several git repositories. Write a "news feed" of the notable activity — new features, fixes, releases. Summarize it into AT MOST 10 punchy headlines (fewer if there's little activity), grouping related commits; each headline up to 15 words. One headline per line. No numbering, no markdown, no preamble.

%s`, b.String())
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		out, err := h.OneShot(ctx, dir, prompt)
		headlines := parseHeadlines(out)
		if len(headlines) > newsMaxHeadlines {
			headlines = headlines[:newsMaxHeadlines]
		}
		return newsFeedMsg{gen: gen, headlines: headlines, err: err}
	}
}

// parseHeadlines turns harness output into clean one-line headlines (dropping
// blanks, fences, and leading bullets).
func parseHeadlines(out string) []string {
	var hs []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		ln = strings.TrimPrefix(ln, "- ")
		ln = strings.TrimPrefix(ln, "* ")
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "```") {
			continue
		}
		hs = append(hs, ln)
	}
	return hs
}

// newsTickCmd schedules the next headline rotation for the given generation.
func newsTickCmd(gen int) tea.Cmd {
	return tea.Tick(newsRotate, func(time.Time) tea.Msg { return newsTickMsg{gen: gen} })
}

// harnessDir is the working directory the harness runs in — the highlighted repo
// (or the first repo). It mostly affects the harness's own ambient context.
func (m Model) harnessDir() string {
	if r := m.currentVisible(m.visibleRepos()); r != nil {
		return r.repo.Path
	}
	if len(m.repos) > 0 {
		return m.repos[0].repo.Path
	}
	return "."
}

// newsDebounceCmd schedules a news refresh a beat after a fetch; a later fetch
// bumps the generation so only the last one in a burst actually refreshes.
func newsDebounceCmd(gen int) tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return newsDebounceMsg{gen: gen} })
}

// maybeRefreshNews starts a news refresh if a harness is available and one isn't
// already in flight; nil otherwise.
func (m *Model) maybeRefreshNews() tea.Cmd {
	if m.newsLoading || !harness.Available(m.cfg.Harness) {
		return nil
	}
	h, ok := harness.ByName(m.cfg.Harness)
	if !ok {
		return nil
	}
	m.newsGen++
	m.newsLoading = true
	return newsRefreshCmd(h, m.harnessDir(), m.newsRepos(), m.cfg.NewsDays, m.newsGen)
}
