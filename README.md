# manygit

A stripped-down, lazygit-style TUI for managing **many git repos at once** —
see at a glance which are in sync / ahead / behind / dirty, and safely fetch,
fast-forward pull, push, or switch branches on the highlighted repo.

## Install

Requires Go 1.24+. (Go's auto-toolchain will fetch 1.24 for you if your host
has Go 1.21+ installed — you don't need to manually upgrade first.)

```bash
go install .            # from a clone, installs `manygit` into $GOBIN
# or
make build && cp manygit ~/bin/
```

> Module path is `manygit` (local). To `go install` by URL, change the module
> path in `go.mod` to your host (e.g. `github.com/blend-ed/manygit`) and update
> internal imports.

## Usage

```bash
manygit                       # scan the current directory
manygit --root ~/work         # scan a specific folder
MANYGIT_ROOT=~/work manygit
```

Discovery walks the root up to `max_depth` (default 3), collecting every
directory containing `.git`, pruning `node_modules`/`vendor`/etc., grouping by
parent directory.

## Keys

Actions apply to the **highlighted** repo (the `>` cursor) — there is no multi-select.

| Key | Action |
|---|---|
| `1` `2` `3`, `tab` | focus the Repos / Branches / Log panel |
| `j`/`k`, `↑`/`↓` | move within the focused panel |
| `space` | jump to the highlighted repo's branches (`space` again returns to Repos) |
| `/` | filter repos by name; `esc` clears |
| `s` | sync the highlighted repo (fetch + `pull --ff-only`; dirty repos skipped) |
| `p` | push the highlighted repo (`git push`) |
| `f` / `r` | fetch the highlighted repo / refetch all |
| `b` / `enter` | checkout the selected branch (in the Branches panel; dirty repos skipped) |
| `o` | open the highlighted repo in `open_cmd` (default `code`) |
| `?` | help overlay (status legend + keys) |
| `q` | quit |

## Status column

| Glyph | Meaning |
|---|---|
| `ok` | up to date with its upstream |
| `^N` | ahead N — commits to **push** |
| `vN` | behind N — commits to **pull** |
| `^N vM` | diverged (N ahead, M behind) |
| `*N` | N files changed (dirty working tree) |
| `~` / `.` | fetching / loading |
| `!` | no upstream, or error |

## Config (optional)

`~/.config/manygit/config.yml`:

```yaml
max_depth: 3
concurrency: 8
open_cmd: code
prune:
  - node_modules
  - vendor
```

manygit never writes to the folder you launch from, and never stashes,
discards, force-pushes, merges, or rebases.
