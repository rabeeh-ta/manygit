// Package discover finds git repositories under a root directory.
package discover

import (
	"os"
	"path/filepath"
	"sort"
)

// Repo is a discovered git repository.
type Repo struct {
	Path  string // absolute path to the repo working tree
	Name  string // base name of Path
	Group string // parent dir relative to root, or "(root)"
}

// Options controls the walk.
type Options struct {
	MaxDepth int
	Prune    map[string]bool
}

// DefaultPrune is the set of directory names never descended into.
func DefaultPrune() map[string]bool {
	set := map[string]bool{}
	for _, n := range []string{
		".git", "node_modules", "vendor", "venv", ".venv",
		"__pycache__", ".tox", ".mypy_cache", ".pytest_cache",
		"dist", "build", ".next", ".cache", "site-packages",
		"target", ".idea", ".vscode",
	} {
		set[n] = true
	}
	return set
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular()
}

func makeRepo(root, dir string) Repo {
	group := "(root)"
	if dir != root {
		if rel, err := filepath.Rel(root, filepath.Dir(dir)); err == nil && rel != "." && rel != "" {
			group = rel
		}
	}
	return Repo{Path: dir, Name: filepath.Base(dir), Group: group}
}

// Discover walks root up to opts.MaxDepth, collecting every directory that
// contains a .git entry. It keeps descending past found repos (so repos nested
// inside a root repo's working tree are found) but never descends into pruned
// directory names, and never follows symlinks.
func Discover(root string, opts Options) ([]Repo, error) {
	root = filepath.Clean(root)
	if opts.Prune == nil {
		opts.Prune = DefaultPrune()
	}

	var repos []Repo
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > opts.MaxDepth {
			return
		}
		if isGitRepo(dir) {
			repos = append(repos, makeRepo(root, dir))
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() { // skips files and symlinks (DirEntry.IsDir is lstat-based)
				continue
			}
			if opts.Prune[e.Name()] {
				continue
			}
			walk(filepath.Join(dir, e.Name()), depth+1)
		}
	}
	walk(root, 0)

	sort.Slice(repos, func(i, j int) bool {
		if repos[i].Group != repos[j].Group {
			return repos[i].Group < repos[j].Group
		}
		return repos[i].Name < repos[j].Name
	})
	return repos, nil
}
