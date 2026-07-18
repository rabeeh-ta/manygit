package tui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/selfupdate"
)

// commitHashRe matches a bullet line that leads with a commit hash — "* <hash>
// message" — capturing the bullet so it can be kept while the hash is dropped.
var commitHashRe = regexp.MustCompile(`^(\s*[*-]\s+)[0-9a-f]{7,40}\s+`)

// EnvUpdatedFrom is set by main.go into the environment of the binary it
// re-execs after a self-update, carrying the version we updated FROM. Its
// presence is the whole trigger for the post-update changelog: only our own
// updater sets it, so a fresh install or `go install` never shows the screen.
const EnvUpdatedFrom = "MANYGIT_UPDATED_FROM"

// changelogCount is how many recent releases the changelog screen pulls. Ten
// covers "everything since you last updated" for anyone who updates with any
// regularity; the collapsed history below the divider makes even ten compact,
// and someone further behind than that doesn't need every ancient tag — the top
// of the list is what changed, which is the point.
const changelogCount = 10

// updatedFrom reads (and clears) the handoff env var. Clearing it means child
// processes manygit spawns — git, gh, scripts — don't inherit a stale trigger,
// and a re-run within the same shell won't re-show the changelog on its own (the
// seen marker guards that case too).
func updatedFrom() string {
	v := strings.TrimSpace(os.Getenv(EnvUpdatedFrom))
	if v != "" {
		os.Unsetenv(EnvUpdatedFrom)
	}
	return v
}

// changelogSeenPath is the marker recording the last from-version whose changelog
// was shown, so the screen appears exactly once per update even if the process is
// restarted with the env var still set. Sits beside the news cache.
func changelogSeenPath() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".cache", "manygit", "changelog-seen")
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "manygit", "changelog-seen")
}

func changelogSeen() string {
	b, err := os.ReadFile(changelogSeenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func markChangelogSeen(from string) {
	p := changelogSeenPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(from), 0o644)
}

// changelogTriggerCmd decides, at startup, whether to fetch the changelog. It
// fires only when the updater handed us a from-version AND that exact version's
// changelog hasn't already been shown. Returns nil (no fetch, no screen)
// otherwise, so a normal launch pays nothing.
func changelogTriggerCmd() tea.Cmd {
	from := updatedFrom()
	if from == "" || from == changelogSeen() {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		rs, err := selfupdate.Releases(ctx, changelogCount)
		return changelogMsg{from: from, releases: rs, err: err}
	}
}

// changelogLines flattens the fetched releases into the scrollable body, newest
// first. Each release is its tag as a heading, then its notes; a divider marks
// the version the user updated FROM, so "everything above this line is new to
// you" reads at a glance. The lines are plain text — the view colours them.
func changelogLines(rs []selfupdate.Release, from string) []string {
	var out []string
	seenFrom := false
	for _, r := range rs {
		// The divider goes just BEFORE the version the user was on: everything
		// above it is new in this update, everything from here down is history he
		// already had. He can still scroll into it — the screen shows the whole
		// changelog, not just the delta, so releases older than `from` are emitted
		// too (without a NEW badge). Once past `from`, no more badges.
		if r.Tag == from && !seenFrom {
			out = append(out, clBody+"", clMark+"— you were on "+from+" · everything above is new —")
			seenFrom = true
		}
		// No blank between releases here — changelogView adds the air before each
		// heading, so the two don't stack into a double gap.
		head := r.Tag
		if d := releaseDate(r.PublishedAt); d != "" {
			head += "  ·  " + d
		}
		if r.Name != "" && r.Name != r.Tag {
			head += "  —  " + r.Name
		}
		// Above the divider = skipped in this update, so NEW. A jump from 1.0.5 to
		// 1.0.7 makes both 1.0.6 and 1.0.7 new. The from-version and older are not.
		kind := clHead
		if !seenFrom {
			kind = clHeadNew
		}
		out = append(out, kind+head)
		// Below the divider is history the user already saw — collapse it to just
		// the version + date. He gets the full browsable version list without a
		// wall of old commit bullets (the earliest releases carry ~50 each). Full
		// notes only for what's NEW, above the divider.
		if seenFrom {
			continue
		}
		for _, ln := range strings.Split(strings.ReplaceAll(r.Body, "\r\n", "\n"), "\n") {
			ln = strings.TrimRight(ln, " \t")
			// Drop the redundant "## Changelog" header goreleaser's default emits —
			// the tag heading already labels the block. Left alone for grouped
			// bodies, which use "Features"/"Fixes" instead.
			if strings.EqualFold(strings.TrimSpace(ln), "## changelog") {
				continue
			}
			// Strip the leading commit hash the old (pre-grouping) release bodies
			// carry: "* 00a9ed6… refactor: x" → "* refactor: x". Grouped bodies have
			// no hash, so this is a no-op on them. Makes every release — including
			// ones frozen before the filter existed — render clean, no GitHub edit.
			ln = commitHashRe.ReplaceAllString(ln, "$1")
			out = append(out, clBody+ln)
		}
	}
	return out
}

// releaseDate pulls the YYYY-MM-DD out of a GitHub ISO-8601 published_at
// ("2026-07-18T08:11:34Z"), or "" if it's missing/short.
func releaseDate(iso string) string {
	if len(iso) >= 10 {
		return iso[:10]
	}
	return ""
}

// Line-kind prefixes: changelogLines tags each line so renderChangelog can colour
// it without re-parsing. They are control bytes that never occur in release text.
const (
	clHead    = "\x00h" // a release heading the user has already seen (the from-version)
	clHeadNew = "\x00H" // a release heading new to the user — rendered with a NEW badge
	clBody    = "\x00b" // a body line
	clMark    = "\x00m" // the "you were here" divider
)
