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
	return strings.TrimSpace(out.String()), nil
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
