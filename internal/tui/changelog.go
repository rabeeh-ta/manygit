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

// commitHashRe matches "* <hash> message", capturing the bullet so the hash can
// be dropped while the message stays.
var commitHashRe = regexp.MustCompile(`^(\s*[*-]\s+)[0-9a-f]{7,40}\s+`)

// EnvUpdatedFrom carries the previous version into the re-exec'd binary after a
// self-update. Only our updater sets it, so it's the trigger that a fresh install
// or `go install` can't fire.
const EnvUpdatedFrom = "MANYGIT_UPDATED_FROM"

// changelogCount caps how many recent releases the screen fetches. Ten covers a
// normal update gap; the collapsed history keeps even that compact.
const changelogCount = 10

// updatedFrom reads and clears the handoff var — cleared so spawned git/gh/script
// processes don't inherit a stale trigger.
func updatedFrom() string {
	v := strings.TrimSpace(os.Getenv(EnvUpdatedFrom))
	if v != "" {
		os.Unsetenv(EnvUpdatedFrom)
	}
	return v
}

// changelogSeenPath records the last from-version shown, so the screen appears
// once per update even across restarts. Sits beside the news cache.
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

// changelogTriggerCmd fetches the changelog only when the updater handed us a
// from-version we haven't already shown; nil otherwise, so a normal launch is free.
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

// changelogLines flattens the releases into the scrollable body, newest first.
// A divider marks the version the user updated from: everything above it is new
// (full notes, NEW badge); everything from there down is history he already had,
// collapsed to just the version + date so the old releases don't dump ~50 commits
// each. The view colours the lines by their kind prefix.
func changelogLines(rs []selfupdate.Release, from string) []string {
	var out []string
	seenFrom := false
	for _, r := range rs {
		if r.Tag == from && !seenFrom {
			out = append(out, clBody+"", clMark+"— you were on "+from+" · everything above is new —")
			seenFrom = true
		}
		head := r.Tag
		if d := releaseDate(r.PublishedAt); d != "" {
			head += "  ·  " + d
		}
		if r.Name != "" && r.Name != r.Tag {
			head += "  —  " + r.Name
		}
		kind := clHead
		if !seenFrom { // above the divider = skipped in this update
			kind = clHeadNew
		}
		out = append(out, kind+head)
		if seenFrom { // collapsed history: heading only
			continue
		}
		for _, ln := range strings.Split(strings.ReplaceAll(r.Body, "\r\n", "\n"), "\n") {
			ln = strings.TrimRight(ln, " \t")
			if strings.EqualFold(strings.TrimSpace(ln), "## changelog") {
				continue // redundant with the tag heading; grouped bodies use Features/Fixes
			}
			// Strip the leading hash old release bodies carry, so pre-filter releases
			// still render clean without a GitHub edit. No-op on grouped bodies.
			ln = commitHashRe.ReplaceAllString(ln, "$1")
			out = append(out, clBody+ln)
		}
	}
	return out
}

// releaseDate pulls YYYY-MM-DD from a GitHub ISO-8601 timestamp, or "" if short.
func releaseDate(iso string) string {
	if len(iso) >= 10 {
		return iso[:10]
	}
	return ""
}

// Line-kind prefixes tag each changelogLines entry so the view can colour it
// without re-parsing. Control bytes that never occur in release text.
const (
	clHead    = "\x00h" // a heading the user already saw (the from-version or older)
	clHeadNew = "\x00H" // a heading new to the user — gets a NEW badge
	clBody    = "\x00b" // a body line
	clMark    = "\x00m" // the "you were here" divider
)
