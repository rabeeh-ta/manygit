// Package git is a thin wrapper over the git CLI. Each function is a pure
// function of a repo path; there is no shared state.
package git

import (
	"bytes"
	"fmt"
	"os/exec"
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
// refs, decorated. Lines are plain (no ANSI); the TUI applies styling.
func GraphLog(dir string, limit int) ([]string, error) {
	out, err := run(dir, "log", "--graph", "--oneline", "--decorate", "--all", fmt.Sprintf("-n%d", limit))
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}
