# manygit

A stripped-down, lazygit-style TUI for managing **many git repos at once** —
see which are in sync / ahead / behind / dirty, and safely fetch, fast-forward
pull, push, or switch branches across a selection.

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

| Key | Action |
|---|---|
| `1` `2` `3`, `tab` | focus Repos / Branches / Log |
| `↑`/`↓`, `k`/`j` | move cursor |
| `J`/`K` | move within Branches panel |
| `space` | toggle-select repo |
| `a` | toggle select-all (visible) |
| `s` | sync selection (fetch + `pull --ff-only`; dirty skipped) |
| `p` | push selection (`git push`) |
| `f` / `r` | fetch highlighted / all |
| `b` / `enter` | checkout the selected branch in the Branches panel (dirty repos skipped) |
| `o` | open highlighted repo in `open_cmd` (default `code`) |
| `/` | filter by name; `esc` clears |
| `q` | quit |

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

manygit never writes to the folder you launch from. Selection is in-memory and
resets each run. It never stashes, discards, force-pushes, merges, or rebases.
