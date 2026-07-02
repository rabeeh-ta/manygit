package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitCmd runs a git command in dir and fails the test on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo creates a repo with one commit on the default branch "master".
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "master")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestStatus_CleanNoUpstream(t *testing.T) {
	dir := initRepo(t)
	st := Status(dir)
	if st.Err != nil {
		t.Fatalf("unexpected err: %v", st.Err)
	}
	if st.Branch != "master" {
		t.Errorf("branch = %q, want master", st.Branch)
	}
	if st.HasUpstream {
		t.Errorf("HasUpstream = true, want false")
	}
	if st.DirtyCount != 0 {
		t.Errorf("DirtyCount = %d, want 0", st.DirtyCount)
	}
}

func TestStatus_Dirty(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := Status(dir)
	if st.DirtyCount != 1 {
		t.Errorf("DirtyCount = %d, want 1", st.DirtyCount)
	}
}

// initRepoWithRemote creates a bare "origin", a clone with one pushed commit,
// and returns (clone, bare).
func initRepoWithRemote(t *testing.T) (clone, bare string) {
	t.Helper()
	bare = t.TempDir()
	gitCmd(t, bare, "init", "-q", "--bare", "-b", "master")

	clone = t.TempDir()
	gitCmd(t, clone, "init", "-q", "-b", "master")
	gitCmd(t, clone, "remote", "add", "origin", bare)
	if err := os.WriteFile(filepath.Join(clone, "a.txt"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, clone, "add", ".")
	gitCmd(t, clone, "commit", "-q", "-m", "c1")
	gitCmd(t, clone, "push", "-q", "-u", "origin", "master")
	return clone, bare
}

// advanceOrigin clones bare, adds a commit, and pushes it.
func advanceOrigin(t *testing.T, bare string) {
	t.Helper()
	other := t.TempDir()
	gitCmd(t, other, "clone", "-q", bare, ".")
	if err := os.WriteFile(filepath.Join(other, "b.txt"), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, other, "add", ".")
	gitCmd(t, other, "commit", "-q", "-m", "c2")
	gitCmd(t, other, "push", "-q", "origin", "master")
}

func TestFetch_DetectsBehind(t *testing.T) {
	clone, bare := initRepoWithRemote(t)
	advanceOrigin(t, bare)
	if err := Fetch(clone); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if st := Status(clone); st.Behind != 1 {
		t.Errorf("Behind = %d, want 1", st.Behind)
	}
}

func TestPullFFOnly_AdvancesHead(t *testing.T) {
	clone, bare := initRepoWithRemote(t)
	advanceOrigin(t, bare)
	if err := Fetch(clone); err != nil {
		t.Fatal(err)
	}
	if err := PullFFOnly(clone); err != nil {
		t.Fatalf("PullFFOnly: %v", err)
	}
	if st := Status(clone); st.Behind != 0 {
		t.Errorf("Behind after pull = %d, want 0", st.Behind)
	}
}

func TestPush_ClearsAhead(t *testing.T) {
	clone, _ := initRepoWithRemote(t)
	if err := os.WriteFile(filepath.Join(clone, "c.txt"), []byte("3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, clone, "add", ".")
	gitCmd(t, clone, "commit", "-q", "-m", "c3")

	if before := Status(clone); before.Ahead != 1 {
		t.Fatalf("Ahead before push = %d, want 1", before.Ahead)
	}
	if err := Push(clone); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if after := Status(clone); after.Ahead != 0 {
		t.Errorf("Ahead after push = %d, want 0", after.Ahead)
	}
}

func TestBranches_ListsLocalAndCurrent(t *testing.T) {
	dir := initRepo(t)
	gitCmd(t, dir, "branch", "feature")

	branches, err := Branches(dir)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	var sawMaster, sawFeature, masterCurrent bool
	for _, b := range branches {
		switch b.Name {
		case "master":
			sawMaster, masterCurrent = true, b.IsCurrent
		case "feature":
			sawFeature = true
		}
	}
	if !sawMaster || !sawFeature {
		t.Errorf("expected master and feature, got %+v", branches)
	}
	if !masterCurrent {
		t.Errorf("master should be current")
	}
}

func TestCheckout_SwitchesBranch(t *testing.T) {
	dir := initRepo(t)
	gitCmd(t, dir, "branch", "feature")
	if err := Checkout(dir, "feature"); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if st := Status(dir); st.Branch != "feature" {
		t.Errorf("branch = %q, want feature", st.Branch)
	}
}

func TestStatusFiles(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := StatusFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = f.Status
	}
	if byPath["a.txt"] != "M" {
		t.Errorf("a.txt status = %q, want M", byPath["a.txt"])
	}
	if byPath["new.txt"] != "??" {
		t.Errorf("new.txt status = %q, want ??", byPath["new.txt"])
	}
}

func TestCommitFiles(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "add b")
	files, err := CommitFiles(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "b.txt" || files[0].Status != "A" {
		t.Errorf("CommitFiles = %+v, want [{A b.txt}]", files)
	}
}

func TestGraphLogEntries(t *testing.T) {
	dir := initRepo(t)
	lines, commits, err := GraphLogEntries(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || len(commits) == 0 {
		t.Fatalf("expected lines and commits, got %d lines, %d commits", len(lines), len(commits))
	}
	for _, c := range commits {
		if c.Line < 0 || c.Line >= len(lines) {
			t.Errorf("commit line index %d out of range", c.Line)
		}
		if len(c.Hash) < 7 {
			t.Errorf("bad hash %q", c.Hash)
		}
	}
}

func TestFileDiffs(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := WorkingFileDiff(dir, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(wd, "\n"), "world") {
		t.Errorf("working diff should show added 'world': %v", wd)
	}
	cd, err := CommitFileDiff(dir, "HEAD", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cd, "\n"), "hello") {
		t.Errorf("commit diff should show 'hello': %v", cd)
	}
}

func TestWorkingFileDiff_Untracked(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("brand\nnew\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := WorkingFileDiff(dir, "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(wd, "\n")
	if !strings.Contains(joined, "new file") || !strings.Contains(joined, "brand") {
		t.Errorf("untracked file diff should show its added content, got:\n%s", joined)
	}
}

func TestWorkingFileDiff_NoCommits(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main") // no commits -> no HEAD
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello there\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := WorkingFileDiff(dir, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(wd, "\n"), "hello there") {
		t.Errorf("a new file in a commitless repo should still show its content: %v", wd)
	}
}

func TestRecentCommits(t *testing.T) {
	dir := initRepo(t) // one commit "init", just made
	all, err := RecentCommits(dir, 5, "")
	if err != nil || len(all) != 1 || all[0] != "init" {
		t.Fatalf("RecentCommits(no window) = %v, %v", all, err)
	}
	recent, err := RecentCommits(dir, 5, "1 day ago") // window includes the fresh commit
	if err != nil || len(recent) != 1 {
		t.Errorf("RecentCommits(1 day) = %v, %v", recent, err)
	}

	// a commit dated long ago is excluded by a short window
	old := t.TempDir()
	gitCmd(t, old, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(old, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, old, "add", ".")
	c := exec.Command("git", "commit", "-q", "-m", "ancient")
	c.Dir = old
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_AUTHOR_DATE=2020-01-01T00:00:00", "GIT_COMMITTER_DATE=2020-01-01T00:00:00")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	if got, _ := RecentCommits(old, 5, "3 days ago"); len(got) != 0 {
		t.Errorf("a 3-day window should exclude a 2020 commit, got %v", got)
	}
	if got, _ := RecentCommits(old, 5, ""); len(got) != 1 {
		t.Errorf("no window should include the old commit, got %v", got)
	}
}

func TestGraphLog_ReturnsCommits(t *testing.T) {
	dir := initRepo(t)
	lines, err := GraphLog(dir, 10)
	if err != nil {
		t.Fatalf("GraphLog: %v", err)
	}
	if len(lines) == 0 {
		t.Fatalf("expected at least one log line")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "init") {
		t.Errorf("log should mention the 'init' commit, got:\n%s", strings.Join(lines, "\n"))
	}
}
