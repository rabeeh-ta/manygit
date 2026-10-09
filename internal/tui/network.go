package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rabeeh-ta/manygit/internal/git"
)

// Shared by Model copies so shutdown also cancels commands queued on the
// semaphore. IDs are allocated on the UI thread, never by background commands.
type networkRuntime struct {
	ctx    context.Context
	cancel context.CancelFunc
	next   uint64
}

func newNetworkRuntime() *networkRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	return &networkRuntime{ctx: ctx, cancel: cancel}
}

// Close cancels network work when the program exits.
func (m Model) Close() {
	if m.network != nil {
		m.network.cancel()
	}
}

func (m *Model) beginNetwork(r *repoVM) (context.Context, uint64) {
	if m.network == nil {
		m.network = newNetworkRuntime()
	}
	m.network.next++
	r.networkID = m.network.next
	r.networkRunning = true
	return m.network.ctx, r.networkID
}

func (m *Model) startFetch(r *repoVM, interactive bool) tea.Cmd {
	if r.networkRunning || r.fetching || m.authRunning {
		return nil
	}
	ctx, id := m.beginNetwork(r)
	r.fetching = true
	path := r.repo.Path
	done := func(err error) tea.Msg {
		return fetchDoneMsg{path: path, id: id, err: err, interactive: interactive}
	}
	if interactive {
		m.authRunning = true
		m.authID = id
		return tea.Exec(&interactiveFetch{ctx: ctx, sem: m.sem, path: path}, done)
	}
	return networkCmd(ctx, m.sem, path, []string{"fetch", "--quiet"}, done)
}

func (m *Model) startSync(r *repoVM) tea.Cmd {
	ctx, id := m.beginNetwork(r)
	path := r.repo.Path
	return networkCmd(ctx, m.sem, path, []string{"pull", "--ff-only", "--quiet"}, func(err error) tea.Msg {
		return syncDoneMsg{path: path, id: id, err: err}
	})
}

func (m *Model) startPush(r *repoVM) tea.Cmd {
	ctx, id := m.beginNetwork(r)
	path := r.repo.Path
	return networkCmd(ctx, m.sem, path, []string{"push", "--quiet"}, func(err error) tea.Msg {
		return pushDoneMsg{path: path, id: id, err: err}
	})
}

func acquireNetwork(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-sem
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func networkCmd(parent context.Context, sem chan struct{}, path string, args []string, done func(error) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if err := acquireNetwork(parent, sem); err != nil {
			return done(err)
		}
		defer func() { <-sem }()
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		defer cancel()
		cmd := git.NetworkCommand(ctx, path, false, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := runNetworkProcess(cmd)
		return done(networkError(ctx, args, err, stderr.String()))
	}
}

func runNetworkProcess(cmd *exec.Cmd) error {
	setProcGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	cleanup := finishProcGroup(cmd)
	defer cleanup()
	return cmd.Wait()
}

func networkError(ctx context.Context, args []string, err error, stderr string) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	stderr = credentialURL.ReplaceAllString(ansi.Strip(stderr), "$1[redacted]@")
	stderr = strings.Join(strings.Fields(stderr), " ")
	return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr)
}

var credentialURL = regexp.MustCompile(`(https?://)[^\s/]*@`)

// Bubble Tea releases the terminal for Run, allowing browser and terminal
// authentication. The timeout starts when Run starts, not when tea.Exec is queued.
type interactiveFetch struct {
	ctx    context.Context
	sem    chan struct{}
	path   string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (c *interactiveFetch) SetStdin(r io.Reader)  { c.stdin = r }
func (c *interactiveFetch) SetStdout(w io.Writer) { c.stdout = w }
func (c *interactiveFetch) SetStderr(w io.Writer) { c.stderr = w }
func (c *interactiveFetch) Run() error {
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Minute)
	defer cancel()
	if err := acquireNetwork(ctx, c.sem); err != nil {
		return err
	}
	defer func() { <-c.sem }()
	args := []string{"fetch", "--quiet"}
	cmd := git.NetworkCommand(ctx, c.path, true, args...)
	cmd.Stdin, cmd.Stdout = c.stdin, c.stdout
	var stderr bytes.Buffer
	if c.stderr == nil {
		cmd.Stderr = &stderr
	} else {
		cmd.Stderr = io.MultiWriter(c.stderr, &stderr)
	}
	// Keep the terminal's process group for interactive reads. CommandContext
	// cancels Git itself; WaitDelay bounds waits on helper-owned output pipes.
	err := cmd.Run()
	return networkError(ctx, args, err, stderr.String())
}

// A result for a removed/re-added repository must never overwrite its new state.
func (m Model) networkRepo(path string, id uint64) *repoVM {
	for _, r := range m.repos {
		if r.repo.Path == path && (id == 0 || r.networkID == id) {
			return r
		}
	}
	return nil
}
