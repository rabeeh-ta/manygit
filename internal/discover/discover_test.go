package discover

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func mkGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func names(repos []Repo) []string {
	var out []string
	for _, r := range repos {
		out = append(out, r.Name)
	}
	sort.Strings(out)
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

func TestDiscover_FindsNestedReposUnderRootRepo(t *testing.T) {
	root := t.TempDir()
	mkGitRepo(t, root)
	mkGitRepo(t, filepath.Join(root, "edx-dev", "blendxapi"))
	mkGitRepo(t, filepath.Join(root, "other", "blendxddn"))

	repos, err := Discover(root, Options{MaxDepth: 3, Prune: DefaultPrune()})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"blendxapi", "blendxddn", filepath.Base(root)}
	sort.Strings(want)
	if !eq(names(repos), want) {
		t.Fatalf("names = %v, want %v", names(repos), want)
	}
}

func TestDiscover_PrunesNodeModules(t *testing.T) {
	root := t.TempDir()
	mkGitRepo(t, filepath.Join(root, "app"))
	mkGitRepo(t, filepath.Join(root, "app", "node_modules", "dep"))

	repos, err := Discover(root, Options{MaxDepth: 5, Prune: DefaultPrune()})
	if err != nil {
		t.Fatal(err)
	}
	if n := names(repos); len(n) != 1 || n[0] != "app" {
		t.Fatalf("names = %v, want [app]", n)
	}
}

func TestDiscover_RespectsMaxDepth(t *testing.T) {
	root := t.TempDir()
	mkGitRepo(t, filepath.Join(root, "a", "b", "c", "deep")) // depth 4
	repos, err := Discover(root, Options{MaxDepth: 3, Prune: DefaultPrune()})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected nothing at depth 4 with MaxDepth 3, got %v", names(repos))
	}
}

func TestDiscover_GroupsByParent(t *testing.T) {
	root := t.TempDir()
	mkGitRepo(t, filepath.Join(root, "edx-dev", "blendxapi"))
	mkGitRepo(t, root)

	repos, err := Discover(root, Options{MaxDepth: 3, Prune: DefaultPrune()})
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]string{}
	for _, r := range repos {
		groups[r.Name] = r.Group
	}
	if groups["blendxapi"] != "edx-dev" {
		t.Errorf("blendxapi group = %q, want edx-dev", groups["blendxapi"])
	}
	if groups[filepath.Base(root)] != "(root)" {
		t.Errorf("root group = %q, want (root)", groups[filepath.Base(root)])
	}
}
