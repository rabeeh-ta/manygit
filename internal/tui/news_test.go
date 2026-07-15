package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// The news-window setting: selecting a day option updates the config (and would
// refresh the feed — no real harness call here since none is installed).
func TestTUI_NewsWindowSetting(t *testing.T) {
	cfg, repos := twoRepos(t)
	cfg.Harness = "definitely-not-installed" // maybeRefreshNews returns nil (no CLI call)
	m := loadAll(t, New(cfg, repos, nil), 100, 30)
	m.showHelp = true
	idx := settingRowIndex(skNewsDays, "7")
	if idx < 0 {
		t.Fatal("expected a 7-day news-window row")
	}
	m.settingsCursor = idx
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.cfg.NewsDays != 7 {
		t.Errorf("selecting 7 days should set NewsDays=7, got %d", m.cfg.NewsDays)
	}
	_ = cmd // no harness → nil; never executed regardless
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

// A fresh on-disk cache is loaded on startup and reused (no re-summarize), so
// opening the app repeatedly doesn't re-run the harness.
func TestNewsCache_LoadFreshIntoModel(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg, repos := twoRepos(t)
	cfg.NewsDays = 3
	sig := repoSig(New(cfg, repos, nil).repos) // deterministic for this repo set

	saveNewsCache(cachedNews{CachedAt: time.Now(), Days: 3, Sig: sig, Headlines: []string{"shipped X", "fixed Y"}})

	m := New(cfg, repos, nil)
	if len(m.newsFeed) != 2 || m.newsFeed[0] != "shipped X" {
		t.Fatalf("a fresh matching cache should load, got %v", m.newsFeed)
	}
	if !m.newsFresh() {
		t.Error("a just-cached feed should be fresh")
	}
	if c := m.maybeRefreshNews(); c != nil {
		t.Error("fresh news should skip the refresh (no harness call)")
	}
}

// A cache is ignored when it's stale, for a different window, or for a different
// repo set — each of those falls through to a normal refresh.
func TestNewsCache_IgnoredWhenStaleOrMismatched(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg, repos := twoRepos(t)
	cfg.NewsDays = 3
	sig := repoSig(New(cfg, repos, nil).repos)
	heads := []string{"x"}

	for name, c := range map[string]cachedNews{
		"stale":       {CachedAt: time.Now().Add(-5 * time.Hour), Days: 3, Sig: sig, Headlines: heads},
		"wrong-days":  {CachedAt: time.Now(), Days: 7, Sig: sig, Headlines: heads},
		"wrong-repos": {CachedAt: time.Now(), Days: 3, Sig: "deadbeef", Headlines: heads},
	} {
		saveNewsCache(c)
		if m := New(cfg, repos, nil); len(m.newsFeed) != 0 {
			t.Errorf("%s cache should be ignored, got %v", name, m.newsFeed)
		}
	}
}

// The cache is a single file that's overwritten, never appended — so storage
// can't grow with every refresh.
func TestNewsCache_Overwrites(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	saveNewsCache(cachedNews{CachedAt: time.Now(), Days: 3, Sig: "a", Headlines: []string{"one"}})
	saveNewsCache(cachedNews{CachedAt: time.Now(), Days: 3, Sig: "a", Headlines: []string{"two", "three"}})
	c, ok := loadNewsCache()
	if !ok || len(c.Headlines) != 2 || c.Headlines[0] != "two" {
		t.Fatalf("cache should hold only the latest write, got %+v ok=%v", c, ok)
	}
}
