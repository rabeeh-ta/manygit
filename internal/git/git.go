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
	HasRemote   bool   // the repo has at least one remote configured
	HasUpstream bool   // the current branch tracks a remote branch
	Slug        string // origin remote as "owner/repo" (for matching GitHub PRs), or ""
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

// MainRef returns the best ref to read the repo's main-branch history from,
// preferring the remote default (so freshly-fetched commits show) then the local
// default: origin/main, origin/master, main, master. "" if none exist.
func MainRef(dir string) string {
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		if refExists(dir, ref) {
			return ref
		}
	}
	return ""
}

func refExists(dir, ref string) bool {
	_, err := run(dir, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// RecentCommits returns up to n of the most recent commit subjects on ref
// (typically the main/master branch — see MainRef), newest first. When since is
// non-empty (a git approxidate like "3 days ago") only commits newer than it are
// returned, so a quiet branch contributes nothing. Empty when ref is "" or has no
// matching commits.
func RecentCommits(dir, ref string, n int, since string) ([]string, error) {
	if ref == "" {
		return nil, nil
	}
	args := []string{"log", ref, "--pretty=format:%s"}
	if n > 0 { // n <= 0 means no count limit (bounded only by --since)
		args = append(args, "-n", strconv.Itoa(n))
	}
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

// Status computes the RepoStatus for the repo at dir.
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

	// A repo with no remote at all is a local-only repo, not a broken one: it has
	// nothing to be ahead of or behind, and sync/push can't work. That is a
	// different state from "has a remote, but this branch was never pushed".
	if remotes, err := run(dir, "remote"); err == nil && remotes != "" {
		st.HasRemote = true
		// Cache the origin slug so the TUI can match a GitHub PR to this repo
		// without a git exec on the checkout keystroke.
		if url, err := run(dir, "remote", "get-url", "origin"); err == nil {
			st.Slug = parseSlug(url)
		}
	}

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

// DiscardTracked hard-resets tracked files to HEAD, reverting all modified and
// staged changes. Untracked (never-committed) files are left in place.
// Destructive and irreversible — callers must confirm first.
func DiscardTracked(dir string) error {
	_, err := run(dir, "reset", "--hard", "HEAD")
	return err
}

// DiscardAll makes the working tree pristine: DiscardTracked plus removing every
// untracked file and directory (git clean -fd). Ignored files (e.g. node_modules,
// .env) are kept. Destructive and irreversible — callers must confirm first.
func DiscardAll(dir string) error {
	if err := DiscardTracked(dir); err != nil {
		return err
	}
	_, err := run(dir, "clean", "-fd")
	return err
}

// Tag is a git tag with the commit it points to and when it was created.
type Tag struct {
	Name    string
	Hash    string // short hash of the tag's target
	Date    string // YYYY-MM-DD it was created
	Subject string // annotation message, or the commit subject for lightweight tags
}

// LatestTag returns the repo's most recent tag name (newest by creation date),
// or "" if it has no tags.
func LatestTag(dir string) (string, error) {
	tags, err := Tags(dir, 1)
	if err != nil || len(tags) == 0 {
		return "", err
	}
	return tags[0].Name, nil
}

// Tags returns up to n of the repo's most recent tags, newest first (by tag
// creation date, falling back to the commit date for lightweight tags).
func Tags(dir string, n int) ([]Tag, error) {
	out, err := run(dir, "for-each-ref",
		fmt.Sprintf("--count=%d", n),
		"--sort=-creatordate",
		"--format=%(refname:short)%09%(objectname:short)%09%(creatordate:short)%09%(contents:subject)",
		"refs/tags")
	if err != nil {
		return nil, err
	}
	var tags []Tag
	for _, ln := range strings.Split(out, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		f := strings.SplitN(ln, "\t", 4)
		t := Tag{Name: f[0]}
		if len(f) > 1 {
			t.Hash = f[1]
		}
		if len(f) > 2 {
			t.Date = f[2]
		}
		if len(f) > 3 {
			t.Subject = f[3]
		}
		tags = append(tags, t)
	}
	return tags, nil
}

// Branch is a local or remote branch of a repo.
type Branch struct {
	Name      string
	IsRemote  bool
	IsCurrent bool
}

// LocalName is the name to check a branch out by. For a remote branch it drops
// only the remote prefix — "origin/feat/x" -> "feat/x", NOT "x" — so git's DWIM
// creates a tracking branch of the right ref; branch names legitimately contain
// slashes (feat/…, fix/…, release/…). Local branches are returned unchanged.
func (b Branch) LocalName() string {
	if !b.IsRemote {
		return b.Name
	}
	if i := strings.Index(b.Name, "/"); i >= 0 {
		return b.Name[i+1:]
	}
	return b.Name
}

// RemoteSlug returns the "owner/repo" of the repo's origin remote, parsed from
// `git remote get-url origin`. It handles https and scp-style ssh URLs, with or
// without a trailing ".git". An error (e.g. no origin remote) or an unparseable
// URL yields "". Used to match a local clone to a GitHub PR's repository.
func RemoteSlug(dir string) (string, error) {
	out, err := run(dir, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return parseSlug(out), nil
}

// parseSlug reduces a git remote URL to "owner/repo" (its last two path
// segments). Pure, so it is unit-testable. Returns "" when the URL has fewer
// than two path segments.
func parseSlug(url string) string {
	s := strings.TrimSpace(url)
	s = strings.TrimSuffix(s, ".git")
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:] // drop scheme
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:] // drop optional user@
		}
		slash := strings.Index(s, "/")
		if slash < 0 {
			return ""
		}
		s = s[slash+1:] // drop host
	} else if colon := strings.Index(s, ":"); colon >= 0 {
		s = s[colon+1:] // scp form host:owner/repo (host may include user@)
	}
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
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
