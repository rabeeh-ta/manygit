package tui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/gh"
	"manygit/internal/git"
)

// statusCmd loads local status (ungated — fast local read).
func statusCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return statusMsg{path: path, st: git.Status(path)}
	}
}

// gated runs fn while holding a semaphore slot.
func gated(sem chan struct{}, fn func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		sem <- struct{}{}
		defer func() { <-sem }()
		return fn()
	}
}

func fetchCmd(sem chan struct{}, path string) tea.Cmd {
	return gated(sem, func() tea.Msg { return fetchDoneMsg{path: path, err: git.Fetch(path)} })
}

func syncCmd(sem chan struct{}, path string) tea.Cmd {
	return gated(sem, func() tea.Msg { return syncDoneMsg{path: path, err: git.PullFFOnly(path)} })
}

func pushCmd(sem chan struct{}, path string) tea.Cmd {
	return gated(sem, func() tea.Msg { return pushDoneMsg{path: path, err: git.Push(path)} })
}

// discardCmd discards a repo's changes: full=true also deletes untracked files
// (D); false reverts only tracked changes (d). Runs only after user confirmation.
func discardCmd(sem chan struct{}, path string, full bool) tea.Cmd {
	return gated(sem, func() tea.Msg {
		var err error
		if full {
			err = git.DiscardAll(path)
		} else {
			err = git.DiscardTracked(path)
		}
		return discardDoneMsg{path: path, full: full, err: err}
	})
}

func latestTagCmd(path string) tea.Cmd {
	return func() tea.Msg {
		tag, err := git.LatestTag(path)
		return latestTagMsg{path: path, tag: tag, err: err}
	}
}

func branchesCmd(path string) tea.Cmd {
	return func() tea.Msg {
		b, err := git.Branches(path)
		return branchesMsg{path: path, branches: b, err: err}
	}
}

// graphCmd loads the colored graph plus its commit entries (line index + hash).
func graphCmd(path string, limit int) tea.Cmd {
	return func() tea.Msg {
		lines, commits, err := git.GraphLogEntries(path, limit)
		return graphMsg{path: path, lines: lines, commits: commits, err: err}
	}
}

// changesCmd loads the changed files of the selected graph entry. ref == ""
// means the working tree (WIP); otherwise a commit hash.
func changesCmd(path, ref string) tea.Cmd {
	return func() tea.Msg {
		var files []git.FileChange
		var err error
		if ref == "" {
			files, err = git.StatusFiles(path)
		} else {
			files, err = git.CommitFiles(path, ref)
		}
		return changesMsg{path: path, ref: ref, files: files, err: err}
	}
}

// diffCmd loads the colored diff of one file for the selected graph entry.
func diffCmd(path, ref, file string) tea.Cmd {
	return func() tea.Msg {
		var lines []string
		var err error
		if ref == "" {
			lines, err = git.WorkingFileDiff(path, file)
		} else {
			lines, err = git.CommitFileDiff(path, ref, file)
		}
		return diffMsg{path: path, ref: ref, lines: lines, err: err}
	}
}

// startScriptCmd runs a script with `bash` in the background (non-interactive),
// merging stdout+stderr into one pipe and reading the first line. The process's
// exit status is delivered to the reader via CloseWithError, so a non-zero exit
// surfaces as scanner.Err() at EOF (no shared state, no race).
func startScriptCmd(path string, run int) tea.Cmd {
	return func() tea.Msg {
		c := exec.Command("bash", path)
		c.Dir = filepath.Dir(path)
		pr, pw := io.Pipe()
		c.Stdout, c.Stderr = pw, pw
		if err := c.Start(); err != nil {
			return scriptOutMsg{run: run, done: true, err: err}
		}
		// Deliver the exit status to the reader: a non-zero exit surfaces as
		// scanner.Err() at EOF. If the TUI quits mid-stream this goroutine is
		// abandoned, but the child then gets SIGPIPE on its next write and exits.
		go func() { pw.CloseWithError(c.Wait()) }()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // tolerate long lines (1 MiB)
		return readScriptLine(sc, run)()
	}
}

// readScriptLine reads the next line from a running script, re-issued after each
// scriptOutMsg to drive the stream until EOF.
func readScriptLine(sc *bufio.Scanner, run int) tea.Cmd {
	return func() tea.Msg {
		if sc.Scan() {
			return scriptOutMsg{run: run, scanner: sc, line: sc.Text()}
		}
		return scriptOutMsg{run: run, scanner: sc, done: true, err: sc.Err()}
	}
}

func checkoutCmd(sem chan struct{}, path, branch string) tea.Cmd {
	return gated(sem, func() tea.Msg {
		return checkoutDoneMsg{path: path, branch: branch, err: git.Checkout(path, branch)}
	})
}

// openWait bounds how long openRepoCmd watches the editor for an IMMEDIATE
// failure. A command still running after it is assumed to have launched (GUI
// editors stay up) and is left alone — never killed.
const openWait = 3 * time.Second

// openRepoCmd launches the editor command (cfg.OpenCmd) on path and reports a
// quick failure instead of failing silently: a command that can't start (e.g.
// not found) or exits non-zero within openWait — like VS Code's "Command is only
// available … inside a Visual Studio Code terminal" when run over plain SSH. A
// command still running after openWait is treated as launched and left running.
func openRepoCmd(openCmd, path string) tea.Cmd {
	return func() tea.Msg {
		prog, args, ok := openArgs(openCmd, path)
		if !ok {
			return openDoneMsg{path: path, err: errors.New("no editor command set (see ? settings)")}
		}
		var out bytes.Buffer
		cmd := exec.Command(prog, args...)
		cmd.Dir = path // run it in the repo, exactly like typing it there
		cmd.Stdout, cmd.Stderr = &out, &out
		if env := editorSocketEnv(); env != "" {
			cmd.Env = append(os.Environ(), env) // let a plain SSH shell reach the editor
		}
		if err := cmd.Start(); err != nil {
			return openDoneMsg{path: path, err: err} // e.g. executable not found
		}
		done := make(chan error, 1) // buffered so the goroutine never leaks
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			// Exited within the window. These editor CLIs are SILENT on success,
			// so any output means trouble — even on a 0 exit: VS Code prints
			// "Command is only available … inside a VS Code terminal" and still
			// exits 0 over plain SSH. Reading out here is race-free (Wait finished
			// the output copy before sending on done).
			if s := strings.TrimSpace(out.String()); s != "" {
				return openDoneMsg{path: path, err: openErr(err, s)}
			}
			if err != nil {
				return openDoneMsg{path: path, err: err}
			}
			return openDoneMsg{path: path}
		case <-time.After(openWait):
			// Still running and silent: a GUI editor that stays attached. Leave it
			// running; don't read out here (the copy goroutine may still write).
			return openDoneMsg{path: path}
		}
	}
}

// openArgs turns the configured open command into an argv for the repo at path.
// The command is the editor invocation as you'd type it in the repo — "code",
// "cursor", "code -r", or "code ." — and manygit supplies the folder itself.
//
// A value that already names a real program is used verbatim, so an absolute path
// with spaces ("/Applications/Visual Studio Code.app/…/bin/code") still works.
// Otherwise it's split into words: first is the program, the rest are flags, and a
// trailing "." is dropped — it means "this folder", which is exactly the path we
// append. ok=false when nothing is configured.
func openArgs(openCmd, path string) (prog string, args []string, ok bool) {
	if strings.TrimSpace(openCmd) == "" {
		return "", nil, false
	}
	if _, err := exec.LookPath(openCmd); err == nil {
		return openCmd, []string{path}, true // a real program, spaces and all
	}
	f := strings.Fields(openCmd)
	if len(f) == 0 {
		return "", nil, false
	}
	prog, args = f[0], f[1:]
	if len(args) > 0 && args[len(args)-1] == "." {
		args = args[:len(args)-1] // "." is the folder we're already appending
	}
	return prog, append(args, path), true
}

// editorSocketEnv returns a "VSCODE_IPC_HOOK_CLI=<socket>" assignment pointing at
// a live editor IPC socket, or "" when there's nothing to do.
//
// Why: the VS Code / Cursor remote CLI can only reach your editor through the
// Remote-SSH server's IPC socket, and the editor injects that socket's path as
// VSCODE_IPC_HOOK_CLI into ITS OWN integrated terminals only. Run manygit from a
// plain SSH shell (iTerm, Terminal) and the variable is absent, so `code <dir>`
// just prints "Command is only available … inside a Visual Studio Code terminal"
// (and still exits 0). The socket itself is reachable from any shell on the box,
// so we find a live one and hand it to the child ourselves.
//
// Returns "" when already inside an editor terminal (nothing to fix) or when no
// live socket exists (no editor window is connected — then nothing could open it
// anyway, and openRepoCmd surfaces the editor's own message).
func editorSocketEnv() string {
	if os.Getenv("VSCODE_IPC_HOOK_CLI") != "" {
		return "" // already in an editor terminal — leave the inherited env alone
	}
	if s := liveEditorSocket(); s != "" {
		return "VSCODE_IPC_HOOK_CLI=" + s
	}
	return ""
}

// liveEditorSocket returns the newest editor IPC socket that still accepts a
// connection, or "". Sockets from closed windows linger on disk but refuse
// connections, so a dial is what separates a live window from a dead one.
func liveEditorSocket() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir() // macOS and others don't set XDG_RUNTIME_DIR
	}
	var socks []string
	// Cursor is a VS Code fork and uses the same hook variable; glob both names.
	for _, pat := range []string{"vscode-ipc-*.sock", "cursor-ipc-*.sock"} {
		if m, err := filepath.Glob(filepath.Join(dir, pat)); err == nil {
			socks = append(socks, m...)
		}
	}
	// Newest first: the most recently opened window is the likeliest target.
	sort.Slice(socks, func(i, j int) bool { return sockModTime(socks[i]).After(sockModTime(socks[j])) })
	for _, s := range socks {
		c, err := net.DialTimeout("unix", s, 200*time.Millisecond)
		if err != nil {
			continue // stale: the window that owned it is gone
		}
		_ = c.Close()
		return s
	}
	return ""
}

func sockModTime(p string) time.Time {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// openErr prefers the editor's own first line of output (the useful message, e.g.
// "Command is only available …") over the bare exit-status error.
func openErr(err error, out string) error {
	out = strings.TrimSpace(out)
	if out == "" {
		return err
	}
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = strings.TrimSpace(out[:i])
	}
	return errors.New(out)
}

// ghProbeCmd resolves gh availability once at startup: gh on PATH AND
// authenticated. Returns an unavailable probe immediately when gh is missing (no
// exec), otherwise makes one `gh api user` call for the login.
func ghProbeCmd() tea.Cmd {
	return func() tea.Msg {
		if !gh.Available() {
			return ghProbeMsg{} // not installed
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		user, ok := gh.Login(ctx)
		return ghProbeMsg{installed: true, available: ok, user: user}
	}
}

// myPRsCmd loads the user's own open PRs (async gh search).
func myPRsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		prs, err := gh.MyOpenPRs(ctx)
		return prsMsg{review: false, prs: prs, err: err}
	}
}

// reviewPRsCmd loads PRs whose review is requested of the user (async gh search).
func reviewPRsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		prs, err := gh.ReviewRequestedPRs(ctx)
		return prsMsg{review: true, prs: prs, err: err}
	}
}

// ghCheckoutCmd checks out PR `number` into its local clone at path. Gated: it
// fetches and moves the working tree, like the other write actions.
func ghCheckoutCmd(sem chan struct{}, path string, number int) tea.Cmd {
	return gated(sem, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return prCheckoutDoneMsg{path: path, number: number, err: gh.Checkout(ctx, path, number)}
	})
}
