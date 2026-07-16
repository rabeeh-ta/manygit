package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/discover"
	"manygit/internal/git"
)

// vm builds a loaded repoVM for the filter tests: a repo named `name` in group
// `group`, sitting on `branch`, with `tag` as its latest tag.
func vm(name, group, branch, tag string) *repoVM {
	return &repoVM{
		repo:      discover.Repo{Path: "/r/" + name, Name: name, Group: group},
		status:    git.RepoStatus{Branch: branch, HasRemote: true, HasUpstream: true},
		loaded:    true,
		latestTag: tag,
	}
}

// filterModel is a Model holding vms with a repo-scoped `/` filter of `needle`.
func filterModel(needle string, vms ...*repoVM) Model {
	return Model{repos: vms, filterPanel: panelRepos, filter: needle}
}

// visibleNames is the names of the repos surviving m's filters, in order.
func visibleNames(m Model) []string {
	var out []string
	for _, r := range m.visibleRepos() {
		out = append(out, r.repo.Name)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// `/` matches the branch shown in the row, not just the folder name: the Repos
// row renders "name (branch)", so /master must find every repo on master.
func TestFilterMatchesBranch(t *testing.T) {
	m := filterModel("master",
		vm("alpha", "grp", "master", "v1.0.0"),
		vm("bravo", "grp", "feature/x", "v2.0.0"),
		vm("charlie", "grp", "master", "v3.0.0"),
	)
	if got, want := visibleNames(m), []string{"alpha", "charlie"}; !eq(got, want) {
		t.Errorf("/master = %v, want %v", got, want)
	}
}

// The folder name still matches — the branch is added to the haystack, not
// swapped in for the name.
func TestFilterStillMatchesName(t *testing.T) {
	m := filterModel("alp",
		vm("alpha", "grp", "master", ""),
		vm("bravo", "grp", "master", ""),
	)
	if got, want := visibleNames(m), []string{"alpha"}; !eq(got, want) {
		t.Errorf("/alp = %v, want %v", got, want)
	}
}

// The tag joins the haystack only while `t` has tags on screen: the filter must
// match what is displayed, and with tags off the tag is not displayed.
func TestFilterMatchesTagOnlyWhenTagsShown(t *testing.T) {
	repos := []*repoVM{
		vm("alpha", "grp", "master", "v1.2.0"),
		vm("bravo", "grp", "master", "v9.9.9"),
	}

	off := filterModel("v1.2", repos...)
	off.showTagsInline = false
	if got := visibleNames(off); len(got) != 0 {
		t.Errorf("/v1.2 with tags hidden = %v, want no matches (tag is not on screen)", got)
	}

	on := filterModel("v1.2", repos...)
	on.showTagsInline = true
	if got, want := visibleNames(on), []string{"alpha"}; !eq(got, want) {
		t.Errorf("/v1.2 with tags shown = %v, want %v", got, want)
	}
}

// A detached HEAD renders as "(detached)", so /detached finds it — the filter
// matching exactly what the row shows.
func TestFilterMatchesDetached(t *testing.T) {
	det := vm("alpha", "grp", "(detached)", "")
	det.status.Detached = true
	m := filterModel("detached", det, vm("bravo", "grp", "master", ""))
	if got, want := visibleNames(m), []string{"alpha"}; !eq(got, want) {
		t.Errorf("/detached = %v, want %v", got, want)
	}
}

// The group header is not part of a repo's row, so it stays out of the haystack
// (`/` and `F` each keep one meaning).
func TestFilterIgnoresGroup(t *testing.T) {
	m := filterModel("infra",
		vm("alpha", "infra", "master", ""),
		vm("bravo", "apps", "master", ""),
	)
	if got := visibleNames(m); len(got) != 0 {
		t.Errorf("/infra = %v, want no matches (group is not searched)", got)
	}
}

// The status cells stay out of the haystack too — `F` owns attention filtering.
func TestFilterIgnoresStatusCells(t *testing.T) {
	m := filterModel("ok", vm("alpha", "grp", "master", ""))
	if got := visibleNames(m); len(got) != 0 {
		t.Errorf("/ok = %v, want no matches (sync glyph is not searched)", got)
	}
}

// `/` and `F` compose (AND): a branch match that is in sync is filtered out by F.
func TestFilterComposesWithAttention(t *testing.T) {
	dirty := vm("alpha", "grp", "master", "")
	dirty.status.DirtyCount = 3
	clean := vm("bravo", "grp", "master", "")

	m := filterModel("master", dirty, clean)
	m.filterAttention = true
	if got, want := visibleNames(m), []string{"alpha"}; !eq(got, want) {
		t.Errorf("/master + F = %v, want %v (only the dirty one)", got, want)
	}
}

// A branch-scoped filter must not narrow the repos list.
func TestBranchScopedFilterLeavesReposAlone(t *testing.T) {
	m := filterModel("master", vm("alpha", "grp", "master", ""), vm("bravo", "grp", "dev", ""))
	m.filterPanel = panelBranches
	if got := visibleNames(m); len(got) != 2 {
		t.Errorf("branches-scoped filter narrowed the repos list to %v", got)
	}
}

// Toggling `t` while a `/` filter is active changes which repos match, because
// the tag joins the haystack. The cursor must stay on the repo it was on rather
// than silently sliding to a different one (which would leave the Branches and
// Log panes describing a repo the cursor no longer highlights).
func TestTagToggleKeepsCursorOnItsRepo(t *testing.T) {
	// With tags hidden, /v1 matches only "v1proxy" by name. Turning tags on adds
	// "alpha" (tag v1.2.0) to the front of the visible list, shifting v1proxy
	// from index 0 to index 1.
	m := filterModel("v1",
		vm("alpha", "grp", "master", "v1.2.0"),
		vm("v1proxy", "grp", "master", "v9.9.9"),
	)
	m.width, m.height = 120, 40

	if got, want := visibleNames(m), []string{"v1proxy"}; !eq(got, want) {
		t.Fatalf("precondition: /v1 with tags hidden = %v, want %v", got, want)
	}
	m.cursor = 0 // on v1proxy

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	got := mm.(Model)

	if !got.showTagsInline {
		t.Fatal("t should turn tags on")
	}
	if names, want := visibleNames(got), []string{"alpha", "v1proxy"}; !eq(names, want) {
		t.Fatalf("after t, /v1 = %v, want %v", names, want)
	}
	cur := got.currentVisible(got.visibleRepos())
	if cur == nil || cur.repo.Name != "v1proxy" {
		name := "<nil>"
		if cur != nil {
			name = cur.repo.Name
		}
		t.Errorf("after t the cursor is on %q, want it still on \"v1proxy\"", name)
	}
}

// keepCursorOn must not reload the context panes when the cursor never left its
// repo. A reload re-fires graphCmd/changesCmd, whose replies reset graphSel,
// graphOffset, changeCursor and changeShowDiff — so a needless one collapses an
// open diff and scrolls the graph back to the top under the user.
func TestKeepCursorOnSkipsReloadWhenCursorStayed(t *testing.T) {
	m := filterModel("master",
		vm("alpha", "grp", "master", ""),
		vm("bravo", "grp", "master", ""),
	)
	m.cursor = 1 // on bravo; /master matches both, so nothing reshuffles

	cmd := m.keepCursorOn("/r/bravo")

	if m.cursor != 1 {
		t.Errorf("cursor moved to %d, want it left on bravo (1)", m.cursor)
	}
	if cmd != nil {
		t.Error("keepCursorOn reloaded context although the cursor never left its repo — " +
			"that reload collapses an open diff and resets the graph scroll")
	}
}

// When the pinned repo is gone from the filtered set the cursor lands on a
// different repo, so the context panes must reload to follow it.
func TestKeepCursorOnReloadsWhenRepoDroppedOut(t *testing.T) {
	m := filterModel("alpha",
		vm("alpha", "grp", "master", ""),
		vm("bravo", "grp", "master", ""),
	)
	// /alpha leaves only alpha visible; bravo is no longer in the set.
	cmd := m.keepCursorOn("/r/bravo")

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want clamped to 0", m.cursor)
	}
	if cmd == nil {
		t.Error("keepCursorOn landed the cursor on a different repo but issued no context reload")
	}
}

// Toggling `t` with no filter active leaves the cursor where it was.
func TestTagToggleWithoutFilterKeepsCursor(t *testing.T) {
	m := filterModel("", vm("alpha", "grp", "master", "v1"), vm("bravo", "grp", "master", "v2"))
	m.width, m.height = 120, 40
	m.cursor = 1

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	got := mm.(Model)

	cur := got.currentVisible(got.visibleRepos())
	if cur == nil || cur.repo.Name != "bravo" {
		t.Errorf("t without a filter moved the cursor off bravo")
	}
}
