package tui

import (
	"strings"
	"testing"
)

func TestParseHeadlines(t *testing.T) {
	got := parseHeadlines("- Feature X shipped\n* Bug Y fixed\n```\n\n  Release Z  \n")
	want := []string{"Feature X shipped", "Bug Y fixed", "Release Z"}
	if len(got) != len(want) {
		t.Fatalf("parseHeadlines = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("headline %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The top bar shows the rotating news feed once headlines arrive, falling back
// to the repo count; ticks rotate; a stale generation is dropped.
func TestTUI_NewsFeed(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, repos, nil), 100, 20)

	if v := stripANSI(m.View()); !strings.Contains(v, "2 repos") {
		t.Error("with no news the top bar should show the repo count")
	}
	// deliver headlines for the current generation → populated + ticker started
	m.newsGen = 1
	mm, cmd := m.Update(newsFeedMsg{gen: 1, headlines: []string{"alpha news", "bravo news", "charlie news"}})
	m = mm.(Model)
	if len(m.newsFeed) != 3 || cmd == nil {
		t.Fatal("matching news should populate the feed and start the ticker")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "news alpha news") {
		t.Errorf("top bar should show the first headline, got: %q", strings.Split(v, "\n")[0])
	}
	// a tick rotates to the next headline
	mm, _ = m.Update(newsTickMsg{gen: 1})
	m = mm.(Model)
	if m.newsIndex != 1 {
		t.Errorf("tick should rotate, index=%d", m.newsIndex)
	}
	// a stale generation is ignored (feed + index unchanged)
	mm, _ = m.Update(newsFeedMsg{gen: 0, headlines: []string{"stale"}})
	m = mm.(Model)
	if len(m.newsFeed) != 3 {
		t.Error("a stale news generation should be dropped")
	}
	mm, _ = m.Update(newsTickMsg{gen: 0})
	m = mm.(Model)
	if m.newsIndex != 1 {
		t.Error("a stale tick should not rotate")
	}
}

func TestMaybeRefreshNews_NoHarness(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := New(cfg, repos, nil)
	m.cfg.Harness = "definitely-not-installed"
	if c := m.maybeRefreshNews(); c != nil {
		t.Error("no harness should not start a news refresh")
	}
	if m.newsLoading {
		t.Error("no refresh should leave newsLoading false")
	}
}
