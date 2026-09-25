# manygit

**A single CLI tool to manage multiple git repositories** — a lazygit-style
terminal UI for a whole **tree** of git repos. Point it at a folder and every
repo underneath is on one screen: its branch, and whether it's ahead / behind /
dirty. Fetch, pull, push, or switch branches on the one under the cursor — plus a
commit graph, a script runner, and a GitHub pull-request pane (when the `gh` CLI
is installed).

Unlike batch runners like `gita` or `mani`, there's no config file to register
repos in and no output to read back: manygit discovers them by walking the
folder, and you stay in the list.

![manygit filtering a tree of repos by branch, browsing pull requests, and switching themes](docs/assets/demo.gif)

<sub>Or **[try the real interface in your browser](https://rabeeh-ta.github.io/manygit/)** — the
landing page runs a working port of the TUI. The git is fake; the keys are the
real keys.</sub>

## Install

**Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/rabeeh-ta/manygit/main/install.sh | bash
```

**macOS**

```bash
brew tap rabeeh-ta/manygit && brew trust rabeeh-ta/manygit && brew install manygit
```

The Linux command works on macOS too, if you'd rather skip Homebrew.

**Windows**

Paste this into **PowerShell** (the default tab in Windows Terminal). It won't
work in Command Prompt.

```powershell
irm https://raw.githubusercontent.com/rabeeh-ta/manygit/main/install.ps1 | iex
```

In Git Bash, MSYS2 or WSL, use the Linux command instead — it installs the same
`manygit.exe`.

Then run `manygit`. The installer scripts put the binary on your PATH and manygit
offers to update itself on launch. A Homebrew or `go install` build is owned by
that package manager, so manygit only tells you when a release is out and leaves
`brew update && brew upgrade --cask manygit` to you.

<details>
<summary>Other ways to install, pinning a version, update details</summary>

**With Go (needs Go 1.24+):**

```bash
go install github.com/rabeeh-ta/manygit@latest
```

**From source:**

```bash
git clone https://github.com/rabeeh-ta/manygit && cd manygit
go build -o ~/.local/bin/manygit .
```

**A specific version** — rolling back, or pinning a machine — is a matter of
passing the tag to the installer:

```bash
curl -fsSL https://raw.githubusercontent.com/rabeeh-ta/manygit/main/install.sh | bash -s -- v1.0.7
```

```powershell
$env:MANYGIT_VERSION = "v1.0.7"; irm https://raw.githubusercontent.com/rabeeh-ta/manygit/main/install.ps1 | iex
```

The self-updater only moves *forward*, so a downgrade goes through the installer,
and the next launch will offer to pull you back to newest — answer `n`, or start
with `--no-update-check` (or `MANYGIT_NO_UPDATE_CHECK=1`) to stay put.

**Where things go:** `install.sh` puts `manygit` in `~/.local/bin`; `install.ps1`
puts `manygit.exe` in `%LocalAppData%\manygit\bin`. Both add that directory to
your PATH if needed.

**Why `brew trust`:** since Homebrew 6, casks from a third-party tap aren't loaded
until you trust the tap, because a cask can run arbitrary Ruby. `brew trust
--cask rabeeh-ta/manygit/manygit` trusts only manygit rather than the whole tap.
When upgrading, the `brew update` matters: a tap is a git clone that
`brew upgrade` alone never pulls, so without it brew reports the stale version
as current.
</details>

## Usage

```bash
manygit                 # scan the current directory
manygit --root ~/work   # scan a specific folder
```

manygit walks the folder (depth 3) for git repos and groups them by parent.

`manygit stats` prints public download counts from GitHub (total releases,
all-time downloads split by OS, and the last 10 tags) — no auth, no telemetry,
just aggregate numbers GitHub already keeps. Anyone can run it.

## Keys

Actions apply to the **highlighted** repo (the `>` cursor).

| Key | Action |
|---|---|
| `1` `2` `3` | focus Repos / Scripts / Branches |
| `4` | PRs — a tab beside Branches (top-right) |
| `5` `6` `7` | bottom slot: Graph / Changes / Output |
| `tab` / `shift+tab` | cycle the panes forwards / backwards |
| `[` / `]` | cycle the focused pane's tabs (3⇄4, 5→6→7) — the numbers still jump straight there |
| `j` `k` | move within the focused panel |
| `→` `←` | hop between Repos and Branches |
| `m` | in the PRs tab: toggle *my PRs* ⇄ *review requests* |
| `enter` | Repos → view branches · Branches → checkout · Scripts → run · PRs → checkout the PR's branch |
| `b` | checkout the highlighted branch — what `enter` does in Branches, from any pane |
| `s` / `p` | sync (fetch + ff-pull) / push the highlighted repo |
| `d` / `D` | discard changes (confirm): `d` tracked only · `D` also deletes untracked files |
| `f` / `r` | fetch one / refetch all |
| `g` | full-screen commit graph |
| `n` | full-screen news feed — all headlines at once (AI-summarized, cached ~4h) |
| `t` | toggle each repo's latest tag inline, after the branch (off by default) |
| `F` | show only repos with changes / ahead / behind |
| `/` | filter the focused list by what it shows — repos match on name **and** current branch (`/master` finds every repo on master, and the tag too while `t` is on); branches match on name (type `feat` to find a remote branch among hundreds); scripts on name |
| `:` | **plain-English git** — type a request (`rebase current onto master`, `sync everything in other/`), your AI harness turns it into git commands, you confirm them, manygit runs them. Repo/folder/branch names autocomplete with `tab`, and `@script.sh` pulls a file in as context (`@scripts/update-all.sh only the frontend apps from this`). Output goes to the Output pane. `↑`/`↓` recall this session's earlier requests; `alt+backspace` / `ctrl+w` delete a word, `ctrl+u` the line |
| `!` | **shell in the highlighted repo** — opens a `$manygit:<group>/<repo>` prompt naming the folder you're in; type a bash command and it runs there (via `cmd.exe` on Windows if bash isn't on PATH), streaming into the Output pane. The prompt **stays open**, so it's a shell you sit in: run, read, run again. The bottom bar shows which repo you're in while you type, so it's never ambiguous. `esc` leaves the shell and lands you back on Repos — it doesn't stop a running command; `ctrl+c` does that. `↑`/`↓` recall this session's earlier commands; `alt+backspace` / `ctrl+w` delete a word, `ctrl+u` the line. Non-interactive, so `vim`, `top` and anything else wanting a terminal won't work |
| `o` | open the repo in your editor |
| `z` | zoom the focused pane |
| `esc` | back out one layer of state: the diff, then Changes, then zoom, then the `/` and `F` filters |
| `?` | help — one overlay, two faces: the keybindings & status legend, and settings (themes, AI harness, news window, scan depth, glyphs, mouse, editor). `tab` / `shift+tab` / `[` / `]` switch faces; `?` or `esc` closes from either |
| `q` | quit |
| `esc esc` | quit with two consecutive presses within 500 ms, from any screen; any other key resets the sequence. The first press shows "Press Esc again to quit" in the footer until the window expires |
| click / wheel | **mouse** (on by default) — click a pane to focus it, a row to move its cursor, a tab chip to switch view; the wheel is `j`/`k` for the pane under the pointer. Navigation only: nothing that changes a repo (checkout, sync, push, discard, run) happens on a click — those stay on the keys. Hold `shift` to select text while it's on, or set **Mouse → off** in `?` to give the mouse back to your terminal |

Status column: `ok` up to date · `↑N` ahead · `↓N` behind · `*N` dirty ·
`no-remote` local-only repo (never pushed anywhere — `s`/`p` skip it) · `!` the
branch has no upstream, or git errored. Set `status_glyphs: ascii` (in config or
`?` then `tab`) if the arrows misalign.

The column stays live without being asked: a PR checkout, a script, or a `:` plan
updates the rows it touches as it touches them, and everything is re-read once
when the run ends.

## GitHub PRs (tab `4`, beside Branches)

If the [`gh` CLI](https://cli.github.com) is installed and signed in
(`gh auth login`), manygit adds a **PRs** tab next to Branches in the top-right
slot (press `4`; `3` switches back to Branches) and shows `github: <user>` next
to the harness in the footer. The tab lists two sets, toggled with `m`:

- **mine** — your open pull requests
- **review requests** — PRs waiting on *your* review

It opens on whichever has something in it; picking one with `m` then sticks.

Each PR takes two lines — number, author and title, then the repo and **what
merges where**:

```
 > #91  @zameel7  Bump the cluster to Kubernetes 1.30
     k8s-manifests (staging): staging ← chore/k8s-1.30
```

The name in parentheses is the branch that clone is on. It goes green once it
matches the PR's branch, and is absent when the repo isn't in your tree.

A compact count of both lists appears at the right end of the top bar. Use `/` to
filter and `j`/`k` to move, like every other list. Press `enter` on a PR to
**check out its branch** in the matching local clone (matched by the origin
remote) — manygit runs `gh pr checkout`, which handles forks and tracking. It
only checks out when the repo is in view and its working tree is clean;
otherwise it says why. `r` refreshes the PR lists along with the repos.

**`enter` leaves you exactly where you are** — focus, both cursors and any active
filter all stay put, so you can walk a PR that spans several repos. The affected
repo's row in the Repos pane updates in place instead.

Needs `gh` installed and signed in. Without it the pane shows a hint and the
top-bar/footer GitHub bits are omitted.

## Config (optional)

`~/.config/manygit/config.yml` (also written by the settings face of `?`):

```yaml
max_depth: 3            # folders below the root to search for repos (1–5 in `?`)
open_cmd: code          # `o` runs this in the repo: code | cursor | code -r | code .
theme: default          # default | serika_dark | dracula | nord | catppuccin | 8008
status_glyphs: unicode  # or "ascii"
mouse: on               # "off" leaves the mouse to your terminal (plain drag-to-select)
```

`max_depth` is also a setting in the `?` overlay — picking a depth re-walks the tree straight
away, no restart. A depth with no repos under it is refused (manygit won't start
on an empty tree, so it won't drop you into one either) and you keep the depth you
had.

`open_cmd` is the editor command as you'd type it in the repo — manygit adds the
folder itself (a trailing `.` is fine). If it can't open, `o` now says why
(e.g. `code` not on PATH). Over SSH, `code`/`cursor` can only reach your editor
through its Remote-SSH server: manygit finds that server's socket automatically,
so `o` works from a plain terminal too **as long as an editor window is connected
to the machine** — otherwise there's nothing to open into.

manygit never writes to the folder you launch from. On its own it never
force-pushes, merges, or rebases — `s` is fetch + fast-forward-only pull and `p`
is a plain push — and the only destructive thing it offers *on its own* is
discarding a repo's changes (`d` / `D`), which always asks you to confirm first.
None of it is reachable by mouse: a click only moves focus, a cursor or a tab, and
clicks are ignored while a confirm is waiting for `y`.

`!` is the deliberate exception: it runs whatever bash command you type in the
highlighted repo (falling back to `cmd.exe` on Windows if bash isn't on PATH),
with no confirm and no blocklist, because a shell escape that second-guessed
you would be useless. What it does instead is leave a record — the command is
echoed into the Output pane with the repo it ran in, before any output
arrives — and `ctrl+c` stops it mid-run.

`:` deliberately widens that, since merging, rebasing and tagging are the point of
it. Three things keep it honest: every command is shown before it runs and needs a
`y`; git is executed with an argument list, never through a shell, so pipes, `&&`
and `rm` cannot be expressed; and force-pushes, remote deletions and the git flags
that take a shell command (`-c`, `rebase --exec`, `submodule foreach`,
`filter-branch`, `bisect run`) are refused before the confirm appears. A batch
stops at the first failure, so a conflict leaves you one repo to fix, not five.

## Releasing (maintainer)

Cut a release by pushing a version tag — GitHub Actions builds the binaries and
publishes the release; the installer and self-updater pick it up automatically:

```bash
git tag v0.2.0
git push origin v0.2.0
```

The version is taken from the tag; nothing in the code needs editing.

The release's **notes become the in-app changelog**: after someone updates
through the built-in updater, manygit fetches the recent releases from the GitHub
API and shows what changed — once, scrollable with `j`/`k`, `esc` to continue. It
is never packaged into the binary. `.goreleaser.yaml` groups the commits into
Features / Fixes from their `feat:` / `fix:` prefixes, so write those and the
changelog writes itself. A fresh install or `go install` never sees the screen —
only an update through the updater triggers it.
