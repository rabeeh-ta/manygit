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
