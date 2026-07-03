# manygit

A lazygit-style terminal UI for managing **many git repos at once**. See every
repo's branch and whether it's ahead / behind / dirty, and fetch, pull, push, or
switch branches on the highlighted one — plus a commit graph, a script runner,
and an AI command helper.

## Install

macOS / Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/rabeeh-ta/manygit/main/install.sh | bash
```

This drops `manygit` into `~/.local/bin` (adding it to your PATH if needed), so
you can run `manygit` from anywhere. On each launch it checks for a newer release
and offers to update itself.

<details>
<summary>From source (needs Go 1.24+)</summary>

```bash
git clone https://github.com/rabeeh-ta/manygit && cd manygit
go build -o ~/.local/bin/manygit .
```
</details>

## Usage

```bash
manygit                 # scan the current directory
manygit --root ~/work   # scan a specific folder
```

manygit walks the folder (depth 3) for git repos and groups them by parent.

## Keys

Actions apply to the **highlighted** repo (the `>` cursor).

| Key | Action |
|---|---|
| `1` `2` `3` | focus Repos / Scripts / Branches |
| `4` `5` `6` `7` | bottom slot: Graph / Changes / Output / Agent |
| `j` `k` | move within the focused panel |
| `space` | Repos → view branches · Scripts → run the script |
| `s` / `p` | sync (fetch + ff-pull) / push the highlighted repo |
| `d` / `D` | discard changes (confirm): `d` tracked only · `D` also deletes untracked files |
| `f` / `r` | fetch one / refetch all |
| `g` | full-screen commit graph |
| `n` | full-screen news feed — all headlines at once |
| `t` | toggle each repo's latest tag inline, after the branch (off by default) |
| `F` | show only repos with changes / ahead / behind |
| `/` | filter the focused list by name |
| `o` | open the repo in your editor |
| `z` | zoom the focused pane |
| `?` | settings & help (themes, AI harness, glyphs, editor) |
| `q` | quit |

Status column: `ok` up to date · `↑N` ahead · `↓N` behind · `*N` dirty · `!` no
upstream. Set `status_glyphs: ascii` (in config or `?`) if the arrows misalign.

## AI agent (`7`)

Tab `7` is a one-shot AI command helper over all your repos. Press `enter` to
type an instruction (e.g. "merge main into the authoring repo"); manygit sends it
to your AI CLI (`claude` or `codex`, picked in `?`) with the workspace context,
shows the git command(s) it proposes, and runs them **only after you confirm**.
Number keys still switch panes while you're on it; `z` zooms for room.

## Config (optional)

`~/.config/manygit/config.yml` (also written by the `?` screen):

```yaml
max_depth: 3
open_cmd: code
theme: default          # default | serika_dark | dracula | nord | catppuccin | 8008
status_glyphs: unicode  # or "ascii"
```

manygit never writes to the folder you launch from, and never force-pushes,
merges, or rebases. The only destructive action is discarding a repo's changes
(`d` / `D`), which always asks you to confirm first.

## Releasing (maintainer)

Cut a release by pushing a version tag — GitHub Actions builds the binaries and
publishes the release; the installer and self-updater pick it up automatically:

```bash
git tag v0.2.0
git push origin v0.2.0
```

The version is taken from the tag; nothing in the code needs editing.
