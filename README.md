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
| `1` `2` `3`, `tab` | focus Repos / Scripts / Branches (tab cycles all slots) |
| `4` `5` `6` | bottom slot: **Graph** / **Changes** / **Output** |
| `j`/`k`, `↑`/`↓` | move within the focused panel |
| `space` | Repos panel → view branches · Scripts panel → run the script · else → back to Repos |
| `enter` | Changes view: open the highlighted file's diff in-place (`esc` = back) |
| `g` | full-screen colored commit graph (`j`/`k` scroll, `esc`/`g` close) |
| `F` | toggle: show only repos with changes / ahead / behind |
| `/` | filter the focused list by name — repos or scripts (whichever pane is focused); `esc` clears |
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

## Bottom panel (Graph / Changes / Output)

The bottom-right slot is a multi-view panel switched with number keys:

- **`4` Graph** — the colored `git log --graph` with a selection cursor. The top
  entry is `WIP (uncommitted changes)`; below it are commits. `j`/`k` move the
  cursor between commits (connector lines are skipped). The selected entry drives
  the Changes view. Long branch names in the ref decorations are shortened so they
  don't push the commit subject off-screen (the Branches panel shows them in full).
- **`5` Changes** — the changed files of the selected graph entry: the working
  tree (when WIP is selected) or a commit's files. `j`/`k` pick a file; `enter`
  opens its colored diff in-place; `esc` returns to the list.
- **`6` Output** — the live combined stdout+stderr of the last script run. Running
  a script (from the Scripts panel) flips the bottom slot here and streams output
  as it arrives, auto-following the tail; `j`/`k` scroll up to stop following. The
  panel title shows the script name and `(running)` until it exits; the status
  line reports success or the failing exit.

## Scripts panel

The `[2] Scripts` panel (below Repos) lists shell scripts found near the root —
root-level and one directory deep (e.g. `scripts/*.sh`), pruning
`node_modules`/`.git`/etc. Both `*.sh` files and extensionless executables with a
`#!` shebang (e.g. `scripts/sync-all`) are listed. The list scrolls to keep the
cursor visible, and `/` filters it by name. Focus it with `2`, move with `j`/`k`,
and press `space` to **run** the highlighted script: manygit runs it with `bash`
in the background (non-interactive) and streams its combined output into the
**Output** view (`6`), which the bottom slot switches to automatically. The status
line reports success or the failing exit when it finishes.

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
