// Package gh is a thin wrapper over the GitHub CLI (`gh`). manygit shells out to
// gh for pull-request info and checkout, reusing gh's own auth — it holds no
// GitHub token of its own (mirroring the harness package's use of the AI CLIs).
//
// Everything degrades gracefully: if gh is missing, logged out, or too old to
// have `gh search prs` (added in gh 2.12), the calls return an error and the TUI
// simply omits the GitHub features. `@me` is a server-side search qualifier, so
// it does not depend on the gh version.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PullRequest is one open pull request as returned by `gh search prs`.
type PullRequest struct {
	Number   int    // PR number within its repo
	Title    string // PR title
	Author   string // author login (author.login)
	RepoSlug string // "owner/repo" (repository.nameWithOwner)
	URL      string // html URL
	IsDraft  bool   // draft PRs are shown dimmed
}

// searchPR is the raw JSON shape of one `gh search prs --json …` element. Only
// the fields we ask for are populated; the rest stay zero.
type searchPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Draft  bool   `json:"isDraft"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

// prJSONFields is the exact --json field set the queries request; it matches the
// searchPR struct so decoding never silently drops data.
const prJSONFields = "number,title,author,repository,isDraft,url"

// run executes gh in dir (or the process cwd when dir=="") and returns trimmed
// stdout, wrapping a non-zero exit with its stderr.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return "", fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// Available reports whether the gh binary is on PATH. It does NOT check auth —
// use Login for that.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// Login returns the authenticated user's login (via `gh api user`) and whether
// the call succeeded. A failure means gh is missing or not logged in; either way
// the GitHub features are unavailable. Preferred over parsing `gh auth status`,
// whose text format has changed across gh versions.
func Login(ctx context.Context) (string, bool) {
	out, err := run(ctx, "", "api", "user", "--jq", ".login")
	if err != nil || out == "" {
		return "", false
	}
	return out, true
}

// MyOpenPRs returns the current user's open PRs (any review state).
func MyOpenPRs(ctx context.Context) ([]PullRequest, error) {
	return searchPRs(ctx, "--author=@me")
}

// ReviewRequestedPRs returns open PRs whose review has been requested from the
// current user.
func ReviewRequestedPRs(ctx context.Context) ([]PullRequest, error) {
	return searchPRs(ctx, "--review-requested=@me")
}

// searchPRs runs `gh search prs` with the given filter plus the shared
// open-state + JSON-field flags, and decodes the result.
func searchPRs(ctx context.Context, filter string) ([]PullRequest, error) {
	out, err := run(ctx, "", "search", "prs", filter,
		"--state=open", "--limit=50", "--json", prJSONFields)
	if err != nil {
		return nil, err
	}
	return parsePRs([]byte(out))
}

// parsePRs decodes the JSON array from `gh search prs --json …` into
// PullRequests. Pure (no exec), so it is unit-testable without a live gh.
func parsePRs(data []byte) ([]PullRequest, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	var raw []searchPR
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("gh: parse prs: %w", err)
	}
	prs := make([]PullRequest, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PullRequest{
			Number:   r.Number,
			Title:    r.Title,
			Author:   r.Author.Login,
			RepoSlug: r.Repository.NameWithOwner,
			URL:      r.URL,
			IsDraft:  r.Draft,
		})
	}
	return prs, nil
}

// Checkout checks out pull request `number` in the repo at dir using
// `gh pr checkout` — which resolves the head branch (including forks) and sets
// up tracking. gh reads the repo from dir's origin remote, so dir must be the
// local clone whose origin matches the PR's repository. The caller must ensure a
// clean working tree; gh refuses a checkout that would clobber local changes.
func Checkout(ctx context.Context, dir string, number int) error {
	_, err := run(ctx, dir, "pr", "checkout", fmt.Sprintf("%d", number))
	return err
}
