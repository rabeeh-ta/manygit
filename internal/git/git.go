// Package git is a thin wrapper over the git CLI. Each function is a pure
// function of a repo path; there is no shared state.
package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// RepoStatus is the computed state of a single repo.
type RepoStatus struct {
	Branch      string
	Default     string
	Upstream    string
	Ahead       int
	Behind      int
	DirtyCount  int
	Detached    bool
	HasUpstream bool
	Err         error
}

// run executes git in dir and returns trimmed stdout.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

func branchExists(dir, branch string) bool {
	if _, err := run(dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return true
	}
	_, err := run(dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	return err == nil
}

func resolveDefault(dir string) string {
	if branchExists(dir, "master") {
		return "master"
	}
	if branchExists(dir, "main") {
		return "main"
	}
	return "master"
}

// Status computes the RepoStatus for the repo at dir.
// RecentCommits returns up to n of the most recent commit subjects across all
// refs (branches + remote-tracking), newest first. When since is non-empty (a
// git approxidate like "3 days ago") only commits newer than it are returned, so
// a quiet repo contributes nothing. Empty for a commitless repo.
func RecentCommits(dir string, n int, since string) ([]string, error) {
	args := []string{"log", "-n", strconv.Itoa(n), "--all", "--pretty=format:%s"}
	if since != "" {
		args = append(args, "--since="+since)
	}
	out, err := run(dir, args...)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func Status(dir string) RepoStatus {
	st := RepoStatus{}

	branch, err := run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		st.Err = err
		return st
	}
	if branch == "HEAD" {
		st.Detached = true
		st.Branch = "(detached)"
	} else {
		st.Branch = branch
	}

	st.Default = resolveDefault(dir)

	if up, err := run(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil && up != "" {
		st.HasUpstream = true
		st.Upstream = up
		if counts, err := run(dir, "rev-list", "--left-right", "--count", up+"...HEAD"); err == nil {
			fields := strings.Fields(counts)
			if len(fields) == 2 {
				st.Behind, _ = strconv.Atoi(fields[0])
				st.Ahead, _ = strconv.Atoi(fields[1])
			}
		}
	}

	if porcelain, err := run(dir, "status", "--porcelain"); err == nil && porcelain != "" {
		st.DirtyCount = len(strings.Split(porcelain, "\n"))
	}

	return st
}

// Fetch updates remote-tracking refs for the default remote (quiet).
func Fetch(dir string) error {
	_, err := run(dir, "fetch", "--quiet")
	return err
}

// PullFFOnly fast-forwards the current branch to its upstream. It never merges
// or rebases; a non-fast-forward returns an error and changes nothing.
func PullFFOnly(dir string) error {
	_, err := run(dir, "pull", "--ff-only", "--quiet")
	return err
}

// Push pushes the current branch to its upstream. Never uses --force.
func Push(dir string) error {
	_, err := run(dir, "push", "--quiet")
	return err
}

// Branch is a local or remote branch of a repo.
type Branch struct {
	Name      string
	IsRemote  bool
	IsCurrent bool
}

// Branches lists local and remote branches (origin/HEAD is skipped).
func Branches(dir string) ([]Branch, error) {
	out, err := run(dir, "branch", "--all", "--format=%(HEAD)\t%(refname)")
	if err != nil {
		return nil, err
	}
	var branches []Branch
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		current := strings.TrimSpace(parts[0]) == "*"
		ref := parts[1]
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			branches = append(branches, Branch{Name: strings.TrimPrefix(ref, "refs/heads/"), IsCurrent: current})
		case strings.HasPrefix(ref, "refs/remotes/"):
			name := strings.TrimPrefix(ref, "refs/remotes/")
			if strings.HasSuffix(name, "/HEAD") {
				continue
			}
			branches = append(branches, Branch{Name: name, IsRemote: true})
		}
	}
	return branches, nil
}

// Checkout switches to branch. For a remote ref "origin/foo", pass "foo" and
// git's DWIM creates a tracking branch. The caller must ensure a clean tree; a
// dirty checkout returns an error and changes nothing.
func Checkout(dir, branch string) error {
	_, err := run(dir, "checkout", branch)
	return err
}

// GraphLog returns up to limit lines of `git log --graph --oneline` across all
// refs, decorated. Lines carry git's own ANSI color (`--color=always`) so each
// branch line/dot is colored, like tig/lazygit; the graph characters are ASCII.
func GraphLog(dir string, limit int) ([]string, error) {
	out, err := run(dir, "log", "--graph", "--oneline", "--decorate", "--all",
		"--color=always", fmt.Sprintf("-n%d", limit))
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// FileChange is one changed file with its short status (e.g. "M", "A", "D", "??").
type FileChange struct {
	Status string
	Path   string
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// GraphEntry marks which rendered graph line index is a commit, and its hash.
type GraphEntry struct {
	Line int
	Hash string
}

// commitHashRe captures the abbreviated hash that follows the graph prefix on a
// commit line (connector lines like "|\" or "| |" have no hash).
var commitHashRe = regexp.MustCompile(`^[\s|/\\_*]*([0-9a-f]{7,40})\b`)

// GraphLogEntries returns the colored graph lines plus, for each line that is a
// commit, its index and hash (so the TUI can put a selection cursor on commits
// and skip connector lines).
func GraphLogEntries(dir string, limit int) (lines []string, commits []GraphEntry, err error) {
	lines, err = GraphLog(dir, limit)
	if err != nil {
		return nil, nil, err
	}
	for i, ln := range lines {
		if m := commitHashRe.FindStringSubmatch(stripANSI(ln)); m != nil {
			commits = append(commits, GraphEntry{Line: i, Hash: m[1]})
		}
	}
	return lines, commits, nil
}

// StatusFiles returns the working-tree changes. Uses `--porcelain -z`: paths are
// NUL-separated and unquoted (so spaces are safe), and a rename's two paths are
// distinct fields rather than an "old -> new" string.
func StatusFiles(dir string) ([]FileChange, error) {
	out, err := run(dir, "status", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var files []FileChange
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		status := strings.TrimSpace(rec[:2])
		files = append(files, FileChange{Status: status, Path: rec[3:]})
		if strings.ContainsAny(status, "RC") {
			i++ // a rename/copy's original path follows in the next field; skip it
		}
	}
	return files, nil
}

// CommitFiles returns the files changed in a commit (git show --name-status).
func CommitFiles(dir, ref string) ([]FileChange, error) {
	out, err := run(dir, "show", "--name-status", "--format=", ref)
	if err != nil {
		return nil, err
	}
	var files []FileChange
	for _, ln := range strings.Split(out, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		parts := strings.Split(ln, "\t")
		if len(parts) < 2 {
			continue
		}
		status := strings.TrimSpace(parts[0])
		if len(status) > 1 {
			status = status[:1] // "R100"/"C75" -> "R"/"C" (drop the similarity %)
		}
		files = append(files, FileChange{
			Status: status,
			Path:   strings.TrimSpace(parts[len(parts)-1]), // last field (handles renames)
		})
	}
	return files, nil
}

// WorkingFileDiff returns the colored diff of a working-tree file vs HEAD.
func WorkingFileDiff(dir, path string) ([]string, error) {
	out, err := run(dir, "diff", "HEAD", "--color=always", "--", path)
	if err != nil || strings.TrimSpace(out) == "" {
		// git diff HEAD shows nothing for untracked files (and errors when the
		// repo has no HEAD yet); show the new file's whole content as an added
		// diff instead of an empty/error view.
		out = runDiff(dir, "diff", "--no-index", "--color=always", "--", "/dev/null", path)
	}
	if strings.TrimSpace(out) == "" {
		return []string{"(no textual changes — empty or binary file)"}, nil
	}
	return strings.Split(out, "\n"), nil
}

// runDiff runs a git diff and returns stdout regardless of exit code: `git diff`
// exits 1 when there ARE differences (esp. --no-index), which is not an error.
func runDiff(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	return strings.TrimRight(out.String(), "\n")
}

// CommitFileDiff returns the colored diff of a file within a commit.
func CommitFileDiff(dir, ref, path string) ([]string, error) {
	out, err := run(dir, "show", "--color=always", ref, "--", path)
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}
