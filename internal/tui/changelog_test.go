package tui

import (
	"github.com/charmbracelet/lipgloss"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/selfupdate"
)

// isolateCache points XDG_CACHE_HOME at a temp dir so the seen-marker read/write
// can't touch a developer's real ~/.cache/manygit.
func isolateCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// The whole trigger contract: fetch the changelog ONLY when the updater set the
// handoff env var, and only when that version hasn't already been seen.
func TestChangelog_TriggerOnlyAfterUpdate(t *testing.T) {
	isolateCache(t)

	// no env var -> normal launch, no fetch
	os.Unsetenv(EnvUpdatedFrom)
	if changelogTriggerCmd() != nil {
		t.Error("a normal launch (no env var) must not fetch the changelog")
	}

	// env var set -> fetch fires
	t.Setenv(EnvUpdatedFrom, "v1.0.0")
	if changelogTriggerCmd() == nil {
		t.Error("an update handoff (env var set) must fetch the changelog")
	}

	// ...and reading it cleared the env var, so a second call in the same process
	// does not re-fire
	if os.Getenv(EnvUpdatedFrom) != "" {
		t.Error("the env var must be cleared once read, so children don't inherit a stale trigger")
	}
	t.Setenv(EnvUpdatedFrom, "v1.0.0")
	markChangelogSeen("v1.0.0") // simulate having shown it already
	if changelogTriggerCmd() != nil {
		t.Error("a version whose changelog was already seen must not fetch again")
	}

	// a DIFFERENT from-version (a later update) fires again
	t.Setenv(EnvUpdatedFrom, "v1.0.5")
	if changelogTriggerCmd() == nil {
		t.Error("a new update (different from-version) must fetch even if an older one was seen")
	}
}

// The message handler shows the screen on a good fetch and marks it seen; on an
// error or empty result it stays down but still records the version so a
// transient failure doesn't re-nag forever.
func TestChangelog_MsgShowsAndMarksSeen(t *testing.T) {
	isolateCache(t)
	cfg, repos := twoRepos(t)

	// good fetch -> screen shows
	m := New(cfg, "", repos, nil)
	rs := []selfupdate.Release{
		{Tag: "v1.0.6", Body: "## Features\n* the changelog screen"},
		{Tag: "v1.0.5", Body: "## Fixes\n* a thing"},
	}
	mm, _ := m.Update(changelogMsg{from: "v1.0.5", releases: rs})
	got := mm.(Model)
	if !got.showChangelog {
		t.Fatal("a good fetch should show the changelog")
	}
	if changelemSeenHelper() != "v1.0.5" {
		t.Errorf("seen marker = %q, want v1.0.5", changelemSeenHelper())
	}
	body := strings.Join(got.changelog, "\n")
	if !strings.Contains(body, "v1.0.6") || !strings.Contains(body, "the changelog screen") {
		t.Error("the changelog body should carry the newest release's notes")
	}
	if !strings.Contains(body, "you were on v1.0.5") {
		t.Error("the from-version marker should be present")
	}

	// error fetch -> no screen, but still marked seen (don't re-nag)
	isolateCache(t)
	m2 := New(cfg, "", repos, nil)
	mm2, _ := m2.Update(changelogMsg{from: "v1.0.5", err: errTest})
	if mm2.(Model).showChangelog {
		t.Error("a failed fetch must not show the screen")
	}
	if changelemSeenHelper() != "v1.0.5" {
		t.Error("even a failed fetch should record the version, so it doesn't retry every launch")
	}
}

// esc dismisses the screen.
func TestChangelog_EscDismisses(t *testing.T) {
	isolateCache(t)
	cfg, repos := twoRepos(t)
	m := New(cfg, "", repos, nil)
	mm, _ := m.Update(changelogMsg{from: "v1.0.5", releases: []selfupdate.Release{{Tag: "v1.0.6", Body: "notes"}}})
	m = mm.(Model)
	if !m.showChangelog {
		t.Fatal("setup: changelog should be showing")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.(Model).showChangelog {
		t.Error("esc should dismiss the changelog")
	}
}

var errTest = &changelogTestErr{}

type changelogTestErr struct{}

func (*changelogTestErr) Error() string { return "boom" }

func changelemSeenHelper() string {
	b, _ := os.ReadFile(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "manygit", "changelog-seen"))
	return strings.TrimSpace(string(b))
}

// The changelog heading carries the release date; NEW marks every version above
// the one the user updated from; the from-version is not NEW and gets the "you
// were here" divider. Body commit-hashes are stripped so old frozen release
// notes still read clean.
func TestChangelog_DatesNewBadgesAndHashStrip(t *testing.T) {
	rs := []selfupdate.Release{
		{Tag: "v1.0.7", PublishedAt: "2026-07-16T08:00:00Z",
			Body: "## Changelog\n* 00a9ed6e2936e1c4a5bbc9a55eb1f21abdbc1c04 feat: streaming"},
		{Tag: "v1.0.6", PublishedAt: "2026-07-10T09:00:00Z", Body: "* fix: a thing"},
		{Tag: "v1.0.5", PublishedAt: "2026-07-01T09:00:00Z", Body: "* older, already seen"},
	}
	lines := changelogLines(rs, "v1.0.5")
	joined := strings.Join(lines, "\n")

	// dates on the tag headings
	if !strings.Contains(joined, clHeadNew+"v1.0.7  ·  2026-07-16") {
		t.Errorf("v1.0.7 heading missing its date; got:\n%s", joined)
	}
	// the from-version is a plain (seen) heading, not NEW
	if !contains(lines, func(s string) bool { return strings.HasPrefix(s, clHead+"v1.0.5") }) {
		t.Error("the from-version should be a seen heading (clHead), not NEW")
	}
	if contains(lines, func(s string) bool { return strings.HasPrefix(s, clHeadNew+"v1.0.5") }) {
		t.Error("the from-version must not be marked NEW")
	}
	// v1.0.7 and v1.0.6 (above the from-version) are NEW
	for _, tag := range []string{"v1.0.7", "v1.0.6"} {
		if !contains(lines, func(s string) bool { return strings.HasPrefix(s, clHeadNew+tag) }) {
			t.Errorf("%s should be marked NEW (it's above the from-version)", tag)
		}
	}
	// the 40-char hash and the redundant "## Changelog" header are gone
	if strings.Contains(joined, "00a9ed6e2936") {
		t.Error("commit hash should be stripped from the body")
	}
	if strings.Contains(joined, "## Changelog") {
		t.Error("redundant '## Changelog' header should be dropped")
	}
	if !strings.Contains(joined, "feat: streaming") {
		t.Error("the actual commit message must survive the hash strip")
	}
	// the marker divides new from already-seen; the from-version and older are
	// still emitted below it (the whole history is scrollable, not just the delta)
	if !strings.Contains(joined, clMark+"— you were on v1.0.5") {
		t.Error("the 'you were here' divider should name the from-version")
	}
	if !contains(lines, func(s string) bool { return strings.HasPrefix(s, clHead+"v1.0.5") }) {
		t.Error("the from-version should still appear below the divider")
	}
	// the marker comes BEFORE the from-version heading
	mi, fi := -1, -1
	for i, l := range lines {
		if strings.HasPrefix(l, clMark) {
			mi = i
		}
		if strings.HasPrefix(l, clHead+"v1.0.5") {
			fi = i
		}
	}
	if mi < 0 || fi < 0 || mi > fi {
		t.Errorf("divider (%d) should sit just above the from-version heading (%d)", mi, fi)
	}
}

// No rendered line may exceed the terminal — the badge and indent must not push
// a heading past the edge at the documented minimum.
func TestChangelog_FitsTerminal(t *testing.T) {
	cfg, repos := twoRepos(t)
	rs := []selfupdate.Release{
		{Tag: "v9.9.9", PublishedAt: "2026-07-18T00:00:00Z", Name: "a fairly long release name here",
			Body: "## Features\n* a reasonably long changelog line that describes a feature in words"},
		{Tag: "v9.9.8", PublishedAt: "2026-07-01T00:00:00Z", Body: "* fix: another line"},
	}
	for _, d := range []struct{ w, h int }{{80, 20}, {100, 30}, {120, 40}} {
		m := loadAll(t, New(cfg, "", repos, nil), d.w, d.h)
		mm, _ := m.Update(changelogMsg{from: "v9.9.8", releases: rs})
		for _, ln := range strings.Split(mm.(Model).changelogView(), "\n") {
			if w := stripANSIWidth(ln); w > d.w {
				t.Errorf("%dx%d: line width %d exceeds terminal %d: %q", d.w, d.h, w, d.w, stripANSI(ln))
			}
		}
	}
}

func contains(ss []string, pred func(string) bool) bool {
	for _, s := range ss {
		if pred(s) {
			return true
		}
	}
	return false
}

func stripANSIWidth(s string) int { return lipgloss.Width(s) }
