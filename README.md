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
| `1` `2` `3` `4`, `tab` | focus the Repos / Scripts / Branches / Log panel |
| `j`/`k`, `↑`/`↓` | move within the focused panel |
| `space` | Repos panel → view branches · Scripts panel → run the script · else → back to Repos |
| `F` | toggle: show only repos with changes / ahead / behind |
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
| `↑N` | ahead N — commits to **push** |
| `↓N` | behind N — commits to **pull** |
| `↑N ↓M` | diverged (N ahead, M behind) |
| `*N` | N files changed (dirty working tree) |
| `~` / `.` | fetching / loading |
| `!` | no upstream, or error |

Ahead/behind use `↑`/`↓` by default. Those are East-Asian *ambiguous* width —
if your terminal renders them two cells wide, the column drifts; set
`status_glyphs: ascii` (below) to use the always-aligned `+N` / `-N` instead.

## Scripts panel

The `[4] Scripts` panel (below Repos) lists `*.sh` files found near the root —
root-level and one directory deep (e.g. `scripts/*.sh`), pruning
`node_modules`/`.git`/etc. Focus it with `4`, move with `j`/`k`, and press
`space` to **run** the highlighted script: manygit suspends, runs it with
`bash` attached to your terminal (so you see its output and can `Ctrl-C` it),
and resumes when it exits. The result shows in the status line.

## Config (optional)

`~/.config/manygit/config.yml`:

```yaml
max_depth: 3
concurrency: 8
open_cmd: code
status_glyphs: unicode   # ahead/behind arrows; use "ascii" for +N / -N if they misalign
prune:
  - node_modules
  - vendor
```

manygit never writes to the folder you launch from, and never stashes,
discards, force-pushes, merges, or rebases.
