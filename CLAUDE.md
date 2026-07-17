# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository.

## What this is

`manygit` — a lazygit-style terminal UI for a whole **tree** of git repos. Point it
at a folder; it walks three levels down (skipping `node_modules`, `vendor`, `dist`,
…), groups every repo it finds by parent, and lets you fetch / pull / push /
checkout the one under the cursor. Go 1.24, Bubble Tea + Lip Gloss, single binary.

Published to GitHub Pages from `docs/` → <https://rabeeh-ta.github.io/manygit/>.

## The rule: the landing-page demo mirrors the TUI

**`docs/` contains a working browser port of the TUI. When you change a feature in
the Go CLI, update the demo in the same change.** It is not a screenshot or a
recording — it is a real reimplementation of the interaction model, and the page
tells visitors "the keys are the real keys". A demo that drifts from the binary
makes the page lie.

This applies to: a new/changed keybinding, a new pane or tab, a renamed pane, a
changed status glyph, a new theme, a new settings row, a changed empty/error
state, or new copy in the footer/help.

### Where things map

| Go (source of truth) | Browser port |
|---|---|
| `internal/tui/update.go` → `handleKey` | `docs/assets/demo.js` → `handleKey` |
| `internal/tui/view.go` → `syncGlyph`, `renderRow`, `tabBar`, `window`, `centerBlock` | `demo.js` → same names, ported deliberately |
| `internal/tui/theme.go` → `themeList` | `docs/assets/site.css` → `:root[data-theme=…]` blocks |
| `internal/tui/settings.go` → `settingRows` | `demo.js` → `settingRows` |
| `internal/discover` → repo/script discovery | `demo.js` → the `REPOS` / `SCRIPTS` fixtures |
| `README.md` key table | `docs/index.html` → the Keys section |

Ported functions keep their Go names on purpose — grep the name in both files.

### Rules the port must keep

- **Copy strings verbatim** from the Go where the demo shows one (empty states,
  status messages, hints). If the Go says `You're all caught up`, so does the demo.
  Don't "improve" it in the port — change the Go, then re-port.
- **Themes are chrome only.** `theme.go` themes `accent/group/dim/error` and
  nothing else; the page does the same. Status colours (`ok`, `↑N`, `*N`, …) stay
  fixed across themes, per theme.go's own comment.
- **`--dim` is not a prose colour.** It's the terminal's chrome colour and fails
  WCAG AA on this background in several themes. Page text uses `--muted`, which is
  each theme's `--dim` lifted to ≥4.5:1. Terminal-internal text keeps `--dim`.
- **Two independent axes: `data-mode` × `data-theme`.** `data-mode` (light/dark)
  is the *ground* and belongs to the site, because manygit has no background
  setting — it inherits your terminal's. `data-theme` is the *chrome* and is
  manygit's. They compose: six themes × two grounds = twelve palettes, and every
  one must clear AA. A new theme means adding **both** a
  `:root[data-theme=…]` block and a `:root[data-mode="light"][data-theme=…]` one
  — theme.go's accents are tuned for a dark terminal and none of them pass on
  paper unmodified. Darken hue-preserving and **measure**; don't eyeball.
- **The demo intentionally diverges in exactly three places**, all because it runs
  in a browser:
  1. `q` explains itself instead of quitting.
  2. `o` explains itself instead of spawning an editor.
  3. **`esc` releases the keyboard when it has nothing else to do.** The widget
     swallows `tab` *and* `shift+tab` (both cycle panes), so without an exit a
     keyboard user is trapped — WCAG 2.1.2. `esc` keeps its real meaning inside
     the Changes pane and any overlay/filter/confirm; only the otherwise-inert
     case is spent on blurring. If you rebind `esc` in the Go, find the demo a new
     exit **and say so in the `aria-label` and the visible cue** — an undisclosed
     escape hatch is the same as none.

  Keep all three, and keep them honest.
- **`runInit()` is a port of `Init()`, not an animation.** First focus replays the
  real launch: every repo unloaded and immediately fetching, so a row goes
  `.` → `~` → its glyph as the local status read lands and then the fetch returns;
  `loadContextCmd` fills Branches/Graph; `ghProbeCmd` reveals the badge and
  `github:`; the harness summarises. Only the *durations* are invented — the
  order is Init's, and the fetch waves are `cfg.Concurrency` (8). Anything gated
  on `repoVM.loaded` in the Go must be gated here too: an unloaded row has no
  branch and no dirty badge, because `r.status` is the zero value until
  `statusMsg` lands.
- The demo fakes git. It must never claim otherwise.

### Verifying a demo change

There are no tests for `docs/` — drive it:

```bash
cd docs && python3 -m http.server 8765   # then open http://localhost:8765
node --check assets/demo.js              # syntax
```

Press the keys you changed. Check the pane still renders, there are no console
errors, and the page has no horizontal overflow at 320px.

## Working conventions

- **Do not run `git` commands that mutate state** (add/commit/branch/push) — the
  user manages all git state. Read-only `status`/`log`/`diff`/`show` is fine.
- `preview-shots/` is gitignored — local screenshots of `docs/`, never committed.
- The site is three static files and needs no build step. Keep it that way: no
  bundler, no framework, all asset paths **relative** (Pages serves it from the
  `/manygit/` subpath, so a leading `/` 404s).

## Releasing

Push a `v*` tag; GitHub Actions + goreleaser build the binaries and publish the
release. The installer and self-updater pick it up. The version comes from the
tag — nothing in the code needs editing.

```bash
git tag v1.0.7 && git push origin v1.0.7
```

## Gotcha: lipgloss `Width` hard-wraps

`lipgloss.NewStyle().Width(n)` does **not** pad-or-overflow — it hard-wraps
anything wider than `n`, mid-word. `keysBody` hit this with `Width(8)` while
`left/right` (10), `no-remote` (9) and `shift+tab` (9) all exceeded it, rendering
the `?` → `tab` screen as:

```
  left/rig
ht      hop between Repos and Branches
```

It's `Width(10)` now, and `TestTUI_KeyColumnFitsEveryLabel` fails if a label ever
outgrows the column again. Keep that in mind anywhere a fixed-width cell holds
caller-supplied text.
