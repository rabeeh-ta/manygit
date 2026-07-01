# manygit — Design Spec

- **Date:** 2026-07-01
- **Status:** Approved (design) — pending implementation plan
- **Author:** Nadheem / Rabeeh (with Claude Code)
- **Location:** `/mnt/datadisk/dev-ulmo/cloned/manygit/`

## 1. Overview

`manygit` is a stripped-down, lazygit-style **terminal UI for managing many git repos at
once**. It answers one question fast — *which of my repos are in sync, ahead, behind, or
dirty?* — and lets you act on them (fetch / pull / push / switch branch) with lazygit-style
keybindings, without the full weight of lazygit's per-repo feature set.

It exists because the workspace holds ~24 independently-versioned repos (Open edX plugins,
MFEs, Hasura layers, tooling). The existing `scripts/repo-status.sh` already prints a static
status table and `scripts/sync-*.sh` already do safe bulk pulls; `manygit` wraps that same
proven logic in an interactive, live-updating TUI and generalizes discovery so it works from
any folder.

### Relationship to existing scripts

`manygit` reuses the exact git logic already validated in this workspace:
- **Status math** from `scripts/repo-status.sh` — default-branch resolution (master→main),
  `git rev-list --left-right --count <upstream>...HEAD` for ahead/behind, dirty detection via
  `git status --porcelain`.
- **Safe-sync behavior** from `scripts/sync-*.sh` — `pull --ff-only`, skip repos with a dirty
  working tree, never stash or overwrite uncommitted work.

`manygit` is not tied to this workspace — it discovers repos under any `--root`.

## 2. Goals / Non-goals

### Goals (v1)
- Instant launch showing cached status for every discovered repo.
- Live, concurrent background `git fetch` so ahead/behind becomes accurate within seconds.
- At-a-glance status: in-sync / ahead / behind / diverged / dirty (with a dirty **count**).
- Safe write actions: fetch, `pull --ff-only`, push, checkout branch — bulk over a selection.
- Per-repo commit **graph/log** view and **branch** list for the highlighted repo.
- Hand-off: open the highlighted repo in an external editor (default VS Code).
- lazygit-style navigation: number keys focus panels, `space` selects, single-letter actions.
- Single static binary, installable via `go install`, runnable from anywhere.

### Non-goals (v1) — YAGNI
- No commits, staging, discarding, or diff-editing (that's what the editor hand-off / real
  lazygit are for).
- No merge, rebase, cherry-pick, or force-push (keeps blast radius small; "Safe sync set").
- No auto-stash — dirty repos are **skipped** for pull/checkout, never touched.
- No submodule / remote / worktree management.
- No mouse support, no config-editing UI, no persistent action history.
- No dedicated "Changes" file-list panel (a dirty **count** in the row is enough — see §5).

## 3. Locked design decisions

| Area | Decision |
|---|---|
| Language / TUI | **Go + Bubble Tea** (lipgloss + bubbles) |
| Fetch model | **Instant open + live background fetch** (concurrent, per-row updates) |
| Write actions | **Safe sync set**: fetch / `pull --ff-only` / push / checkout; dirty repos skipped |
| Changes view | **Row-level dirty count** (`●N`), no file-list panel |
| Hand-off | Open highlighted repo in `open_cmd` (default `code`), spawned detached |
| Discovery | **Recursive from `--root`/cwd, max depth 3, prune junk dirs** |
| Distribution | Standalone repo, single binary via `go install` |

## 4. Discovery model

- **Root selection:** `--root DIR` flag → else `$MANYGIT_ROOT` → else current working directory.
- **Depth-limited walk:** DFS from root, `max_depth` (default **3**; root = depth 0). Configurable.
- **Repo = any directory containing `.git`** (a directory *or* a file — the file form covers
  worktrees/submodules).
- **Keep descending past a found repo.** Critical: the workspace root is itself a git repo and
  the real targets live *inside* its working tree. A naive "stop at first `.git`" would find
  only the root. So we continue walking into non-pruned children up to `max_depth`.
- **Prune by directory name** (never descend into these). Default prune set:
  `.git`, `node_modules`, `vendor`, `venv`, `.venv`, `__pycache__`, `.tox`, `.mypy_cache`,
  `.pytest_cache`, `dist`, `build`, `.next`, `.cache`, `site-packages`, `target`, `.idea`,
  `.vscode`. Configurable (add/remove).
  - *On `node_modules`:* npm strips `.git` from published packages so there's usually none, but
    git-installed deps can carry one — pruning by name makes this safe **and** fast regardless.
- **Symlinks are not followed** (avoids cycles).
- **Grouping:** repos are grouped by their **parent directory relative to root** (so pointing at
  the workspace yields `edx-dev` / `tutor-mfe` / `other` headers automatically). A repo at the
  root itself falls under a `(root)` group. Groups and repos are sorted alphabetically.

## 5. Status model

Per repo, computed from git (mirrors `repo-status.sh`):
- **Current branch** (`rev-parse --abbrev-ref HEAD`, or `(detached)`).
- **Default branch** (`master` if present, else `main`, else `master`).
- **Upstream** (`rev-parse --abbrev-ref --symbolic-full-name @{u}`), may be absent.
- **Ahead / behind vs upstream** (`rev-list --left-right --count @{u}...HEAD`).
- **Dirty count** = number of changed paths from `git status --porcelain` (staged + modified +
  untracked, combined).

### Row glyphs

| Glyph | Meaning | Color |
|---|---|---|
| `✓` | clean & up to date with upstream | green |
| `↑N` | ahead N — push pending | yellow |
| `↓N` | behind N — pull available | cyan |
| `⇕ ↑N ↓M` | diverged (ahead **and** behind) | magenta |
| `●N` | dirty working tree, N changed paths (separate field, shown alongside sync state) | orange |
| `⟳` | fetch in flight | dim |
| `⚠` | no upstream / error (message shown in status line) | red |

Row format: `<sel> <sync-glyph> <name>   <●N?>  <↑/↓ counts>`

## 6. UI layout & panels

```
┌─1 Repos ──────────────────────┐┌─2 Branches (blendxapi) ──────┐
│ edx-dev                       ││   master            ← current│
│  ✓ ai_course_creator          ││   ulmo-update                │
│ ▸● blendxapi     ●3   ↑2      ││   origin/master              │
│  ✓ blendxai                   │└──────────────────────────────┘
│  ⟳ blendxddn     fetching…    │┌─3 Log ───────────────────────┐
│ tutor-mfe                     ││ * a1b2c3 (HEAD) fix webhook   │
│ [✓]frontend-app-authoring     ││ * d4e5f6 add credits gate     │
│  ↓ frontend-app-learning  ↓5  ││ *   7890ab Merge origin/mast… │
│ other                         ││ |\                            │
│  ⇕ blendxmetadata   ↑1 ↓3     ││ | * bc1234 hasura migration   │
└───────────────────────────────┘└───────────────────────────────┘
 space select · s sync · p push · b checkout · o open · r refetch · ? help
```

- **Panel 1 — Repos** (left, full height): all repos grouped by section, each with sync glyph,
  dirty count, ahead/behind. `▸` = cursor; `[✓]` = multi-selected.
- **Panel 2 — Branches** (right, top): local + remote branches of the *highlighted* repo;
  `enter` checks one out.
- **Panel 3 — Log** (right, bottom): `git log --graph --oneline --all --decorate` for the
  highlighted repo, colorized, lazily loaded.
- Number keys `1` `2` `3` focus a panel; `tab` cycles. Right panels always reflect the repo
  under the cursor in panel 1.

### 6.1 Layout & spacing discipline (hard requirement)

Past TUIs on this stack drifted on margins/padding/alignment. These rules are mandatory and
enforced by a test:

- **Measure only with `lipgloss.Width()` — never `len()`.** ANSI escape codes and wide glyphs
  (`↑ ↓ ⇕ ● ⟳ ✓`) make byte/rune length wrong for alignment.
- **No manual space-padding (`%-20s`, `strings.Repeat`, `TrimRight`) for columns.** Every column
  is a fixed-width `lipgloss.NewStyle().Width(n)` cell; rows are composed from those cells so a
  2-cell-wide glyph never shifts the layout.
- **All sizing derives from `tea.WindowSizeMsg`** through a single `computeDims(width,height)` in
  `layout.go`; panels recompute on resize. No hard-coded widths in render code.
- **Panels use one `panelStyle()` helper** (rounded border + `Padding(0,1)`), and the border/
  padding cells are budgeted in the width math so two panels + gutter exactly fit the terminal.
- **Centralized styles/colors** live in `styles.go` (no inline style literals scattered in views).
- **Status glyphs stay concise** (`✓`, `↑N`, `↓N`, `⇕↑N↓M`, `●N`, `⟳`, `⚠`); long messages go to
  the status line, never into a row column.
- **Regression guard:** a `teatest`/unit test renders at a fixed terminal size and asserts every
  output line's `lipgloss.Width` ≤ terminal width.

## 7. Keybindings

| Key | Action |
|---|---|
| `1` / `2` / `3`, `tab` | focus Repos / Branches / Log (tab cycles) |
| `↑`/`↓` or `k`/`j` | move within focused panel |
| `space` | toggle-select the highlighted repo |
| `a` | toggle select-all (currently visible / filtered) |
| `s` | **sync** selection (fetch + `pull --ff-only`); no selection → highlighted repo |
| `p` | **push** selection / highlighted (`git push`, no force) |
| `f` / `r` | fetch highlighted / refetch **all** |
| `b` or `enter` (panel 2) | checkout highlighted branch |
| `o` | open highlighted repo in `open_cmd` (default `code`) |
| `/` | filter repos by name; `esc` clears |
| `?` / `q` | help overlay / quit |

### Action semantics & safety
- **Sync** operates on the **current branch's upstream** (`pull --ff-only`) — it does *not* force
  a reset to the default branch. A repo with a dirty working tree is **skipped** with a message
  in the status line; nothing is stashed or overwritten. Detached HEAD / no upstream → skipped.
- **Push** is a plain `git push` (never `--force`). If no upstream is set, the action fails
  gracefully and the status line hints to set one; `manygit` does not auto-create upstreams in v1.
- **Checkout** is skipped for dirty repos (same rule as sync).
- **Open** spawns `open_cmd <repo-path>` detached (VS Code opens a window and returns); the TUI
  is not suspended.

## 8. Architecture

Go module `manygit` with a thin, testable core and a Bubble Tea front end.

```
manygit/
  main.go                 # flags (--root, --version), config load, discover, run TUI
  internal/
    config/               # load ~/.config/manygit/config.yml (+ baked-in defaults)
    git/                  # os/exec wrapper over the git CLI (pure funcs over a repo path)
    discover/             # depth-limited, prune-aware repo discovery + grouping
    tui/                  # Bubble Tea Model, messages, commands, rendering
  Makefile
  README.md
```

- **`internal/git`** — `git -C <path> …` via `os/exec`. Functions: `Status`, `Fetch`,
  `PullFFOnly`, `Push`, `Branches`, `Checkout`, `GraphLog`. No global state; each is a pure
  function of a repo path, independently unit-testable.
- **`internal/discover`** — `Discover(root, cfg) → []Repo{Path, Name, Group}`; the depth-limited
  prune-aware walk from §4.
- **`internal/tui`** — the Bubble Tea `Model`: repo view-models (status + per-repo async state),
  cursor, focused panel, selection set, filter string, cached branches/log for the highlighted
  repo, and a status line. Git operations run as `tea.Cmd` goroutines returning typed messages
  (`statusLoadedMsg`, `fetchDoneMsg`, `syncDoneMsg`, `pushDoneMsg`, `branchesLoadedMsg`,
  `logLoadedMsg`, `errMsg`). **Concurrency is capped by a shared worker semaphore** (buffered
  channel, size = `config.concurrency`, default 8) so we never spawn 24 git processes at once.
  On `Init`, the model batches a status-load for every repo plus a background fetch for every
  repo (fetches gated by the semaphore), which realizes "instant open + live background fetch".
- **`internal/config`** — loads config with baked-in defaults; file is optional.

## 9. Configuration

`~/.config/manygit/config.yml` (all optional; sensible zero-config defaults):

```yaml
root: null                 # default: --root flag → $MANYGIT_ROOT → cwd
max_depth: 3
concurrency: 8
open_cmd: code             # editor hand-off; could be lazygit, $EDITOR, a shell, etc.
prune:                     # merged with / overrides the default prune set
  - node_modules
  - vendor
  - .venv
```

Zero-config: run `manygit` inside the workspace and it scans down to depth 3, pruning junk,
grouping by parent dir — no config file required.

### 9.1 State & persistence

**Principle: nothing is ever written into the folder you launch from.** Your repo/working
directories stay clean — no `.manygit/`, no `.manygit.json`, no `.gitignore` churn.

- **Config** (settings) — global and optional: `$XDG_CONFIG_HOME/manygit/config.yml`
  (default `~/.config/manygit/config.yml`). Exists only if you choose to override defaults.
- **Selection / cursor / filter** — **in-memory only; resets every launch** (v1), like lazygit.
  No selection state is persisted anywhere.
- **No central state or cache files in v1.** (A per-root state store under
  `$XDG_STATE_HOME/manygit/` was considered and explicitly deferred — see §13.)

So the complete on-disk footprint of `manygit` is at most a single optional global config file.

## 10. Error handling

- Per-repo failures render inline (`⚠` glyph + message in the bottom status line); one broken
  repo never crashes the app.
- Fetch/push failures are non-fatal and retryable (`f` / `p`).
- Dirty-skip and no-upstream conditions surface as status-line messages, not errors.

## 11. Testing strategy

- **`git` package:** spin up throwaway repos with `t.TempDir()` + `git init`, add commits and a
  bare "origin" remote; assert `Status` ahead/behind/dirty, `PullFFOnly`, `Push`, `Branches`,
  `Checkout`.
- **`discover` package:** build temp directory trees with nested `.git` dirs and `node_modules`;
  assert the found set respects depth limits and prune rules.
- **`tui` package:** drive the Bubble Tea model with the `teatest` harness — send key messages,
  assert model transitions and rendered output.

## 12. Distribution

- `go install <module-path>/manygit@latest` → single static binary in `$GOBIN`, callable from
  anywhere.
- `Makefile`: `build`, `install`, `test`, `lint`, `run`.
- `README.md` documents keybindings, config, and the discovery model.

## 13. Future (explicitly deferred)

- Actionable Changes panel (stage / discard / diff).
- `vs default branch` divergence detail (the `repo-status.sh` "vs master/main" column).
- More actions behind a "power mode" (merge / rebase / force-with-lease).
- Auto-stash option around pull/checkout.
- Per-root persisted state (remember selection/cursor/filter per workspace) under
  `$XDG_STATE_HOME/manygit/<root-hash>.json` — central, never a dotfile in the working tree.
- Mouse support and a config-editing screen.
