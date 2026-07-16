# `/` filters what the row shows, not just the folder name

**Date:** 2026-07-16
**Status:** approved, ready to implement

## Problem

In the Repos panel each row renders as `name (branch)` — and as `name (branch) (tag)`
while `t` has tags inline. But `/` only ever matches `r.repo.Name`
([view.go:193](../internal/tui/view.go#L193)), so typing `/master` finds nothing even
though `(master)` is visible on screen. The filter disagrees with the display.

## Goal

`/` matches the text the row shows: the repo name, the current branch, and — only
while `t` has tags displayed — the latest tag.

`/master` narrows to every repo sitting on master. `/v1.2` narrows by tag, but only
when tags are on screen.

## Non-goals

- The group header and the status cells (`*3`, `ok`, `↑2`, `no-remote`) stay
  unmatched. `F` already filters by attention state; overlapping it with `/` would
  make `/ok` mean two things.
- The Branches, Scripts, and PR filters are unchanged — each already matches what
  its own panel displays.

## Design

### Haystack

A helper beside `visibleRepos`, mirroring how `visiblePRs`
([model.go:218](../internal/tui/model.go#L218)) already concatenates slug + title +
author:

```go
// repoHaystack is the text `/` matches a repo row against: what the row shows —
// its name and current branch, plus the latest tag while `t` has tags inline.
// It matches the full values rather than the width-truncated ones renderRow
// draws, so results never depend on how wide the terminal happens to be.
func (m Model) repoHaystack(r *repoVM) string {
	s := r.repo.Name + " " + currentBranch(r.status)
	if m.showTagsInline {
		s += " " + r.latestTag
	}
	return strings.ToLower(s)
}
```

`visibleRepos` then tests `strings.Contains(m.repoHaystack(r), needle)` in place of
the name-only check. Everything else there — the `needle == ""` fast path, the `F`
composition — is untouched.

### Full values, not truncated ones

`fitNameSuffixes` truncates or drops the branch when the name column is narrow. The
haystack deliberately uses the untruncated branch and tag: matching the literal
pixels would make `/master` return different repos at different terminal widths, and
resizing the window would silently change results.

Consequence, and it is the right one: `currentBranch` returns `"detached"` for a
detached HEAD, so `/detached` finds detached repos — exactly what the row shows.

### Keeping the cursor on its repo when `t` toggles

This is the part the feature forces. Today `showTagsInline` only affects *drawing*,
so `t` can never change which repos are listed. Once the tag feeds the filter, `t`
resizes the visible list underneath the cursor: press it with `/v1.2` active and the
cursor silently lands on a **different repo** while the Branches and Log panes keep
showing the old one.

So the `t` handler pins the cursor to the repo it was on:

```go
func (m *Model) keepCursorOn(path string) tea.Cmd {
	m.cursor = 0
	for i, r := range m.visibleRepos() {
		if r.repo.Path == path {
			m.cursor = i
			break
		}
	}
	if r := m.currentVisible(m.visibleRepos()); r != nil && r.repo.Path == path {
		return nil // same repo still under the cursor — nothing to reload
	}
	return m.loadContextCmd()
}
```

Called from `t` only when a repo-scoped `/` filter is actually active, so an
unfiltered `t` doesn't pay for a needless context reload.

**The reload must be conditional**, which the first draft of this design got
wrong. `loadContextCmd` re-fires `graphCmd`/`changesCmd`, and their replies reset
`graphSel`, `graphOffset`, `changeCursor` and `changeShowDiff`. So reloading on
every `t` — including the common case where the visible set doesn't move, e.g.
`/master` where every match matches with or without tags — would collapse an open
diff and scroll the graph back to the top. Before this feature `t` only affected
drawing and issued no reload at all; returning `nil` when the cursor stayed put
preserves that.

`focusRepoByPath` is not reusable here — it clears the filter, and this case must
preserve it.

### Known gap, deliberately left

Tags load asynchronously (`loadTagsCmd` → `latestTagMsg`), so with tags on and a
filter typed, repos pop into the list as tags arrive and the cursor can drift. This
exact bug already exists for `F`, whose `needsAttention` keys off `r.status` —
which also streams in. Matching the existing behavior beats fixing half of it here;
worth its own change if it ever bites.

## Tests

- `/master` matches by branch, not just name
- tag matches only while `showTagsInline` is on
- plain name matching still works (regression)
- `/` and `F` still AND together
- `/detached` finds a detached HEAD
- `t` with a filter active keeps the cursor on its repo
