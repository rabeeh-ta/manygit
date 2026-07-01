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

var _ = strings.TrimSpace // strings used by later tests in this file

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
