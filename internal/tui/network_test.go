package tui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rabeeh-ta/manygit/internal/config"
	"github.com/rabeeh-ta/manygit/internal/discover"
)

func networkModel(t *testing.T) Model {
	t.Helper()
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	t.Cleanup(m.Close)
	return m
}

func TestNetworkErrorsAndExplicitLogin(t *testing.T) {
	m := networkModel(t)
	r := m.repos[0]
	m.startFetch(r, false)
	id := r.networkID
	err := errors.New("connection refused")
	mm, _ := m.Update(fetchDoneMsg{path: r.repo.Path, id: id, err: err})
	m = mm.(Model)
	if r.networkErr != err || r.fetching || r.networkRunning || !needsAttention(r) {
		t.Fatal("failed fetch did not retain error or release repository")
	}
	if stripANSI(syncGlyph(r, false)) != "!" || !strings.Contains(m.statusOrFilterLine(), "connection refused") {
		t.Fatal("fetch failure is not visible")
	}
	// A local-status update must not erase the network error.
	mm, _ = m.Update(statusMsg{path: r.repo.Path, st: r.status})
	m = mm.(Model)
	if r.networkErr != err {
		t.Fatal("local status erased network error")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = mm.(Model)
	if m.bottomView != bvOutput || m.outputRunning || !strings.Contains(strings.Join(m.outputLines, "\n"), "connection refused") {
		t.Fatal("network error details unavailable in Output")
	}
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = mm.(Model)
	if cmd == nil || !m.authRunning || !r.fetching {
		t.Fatal("f on failed repo did not start interactive retry")
	}
	if m.startFetch(m.repos[1], true) != nil {
		t.Fatal("multiple interactive retries allowed")
	}
	mm, cmd = m.Update(tea.FocusMsg{})
	m = mm.(Model)
	if cmd != nil {
		t.Fatal("focus event launched work during authentication")
	}
	mm, _ = m.Update(fetchDoneMsg{path: r.repo.Path, id: r.networkID, interactive: true, err: context.Canceled})
	m = mm.(Model)
	if m.authRunning || r.fetching || r.networkErr == nil || m.repos[1].fetching {
		t.Fatal("cancelled authentication was automatically retried")
	}
}

func TestLoginRetriesFailedFetchesIncludingLateResults(t *testing.T) {
	m := networkModel(t)
	a, b := m.repos[0], m.repos[1]
	m.startFetch(b, false)
	oldID := b.networkID
	m.startFetch(a, true)
	mm, _ := m.Update(fetchDoneMsg{path: a.repo.Path, id: a.networkID, interactive: true})
	m = mm.(Model)
	mm, _ = m.Update(fetchDoneMsg{path: b.repo.Path, id: oldID, err: errors.New("login required")})
	m = mm.(Model)
	if !b.fetching || b.networkID <= oldID {
		t.Fatal("failure queued during login was not retried")
	}
	retryID := b.networkID
	mm, _ = m.Update(fetchDoneMsg{path: b.repo.Path, id: retryID, err: errors.New("permission denied")})
	m = mm.(Model)
	if b.fetching || b.networkID != retryID || b.networkErr == nil {
		t.Fatal("failed silent retry must stop after one attempt")
	}
	// A late completion from the old attempt cannot clear this failure.
	mm, _ = m.Update(fetchDoneMsg{path: b.repo.Path, id: oldID})
	m = mm.(Model)
	if b.networkErr == nil {
		t.Fatal("stale completion overwrote new error")
	}
	// Already completed failures get the same silent retry treatment.
	m.startFetch(a, true)
	mm, _ = m.Update(fetchDoneMsg{path: a.repo.Path, id: a.networkID, interactive: true})
	m = mm.(Model)
	if !b.fetching || b.networkID <= retryID {
		t.Fatal("existing fetch failure was not retried after login")
	}
}

func TestNetworkRemovedAndReaddedRepo(t *testing.T) {
	m := networkModel(t)
	r := m.repos[0]
	m.startFetch(r, false)
	id, path := r.networkID, r.repo.Path
	m.repos = m.repos[1:]
	mm, cmd := m.Update(fetchDoneMsg{path: path, id: id, err: errors.New("old")})
	m = mm.(Model)
	if cmd != nil {
		t.Fatal("removed repository result scheduled work")
	}
	replacement := &repoVM{repo: discover.Repo{Path: path}}
	m.repos = append(m.repos, replacement)
	m.startFetch(replacement, false)
	mm, _ = m.Update(fetchDoneMsg{path: path, id: id})
	m = mm.(Model)
	if !replacement.fetching {
		t.Fatal("old result completed replacement repository's fetch")
	}
}

func TestNetworkBusyRepoCannotSyncOrPush(t *testing.T) {
	for _, key := range []string{"f", "r", "s", "p"} {
		t.Run(key, func(t *testing.T) {
			m := networkModel(t)
			r := m.repos[0]
			m.startFetch(r, false)
			id := r.networkID
			mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			m = mm.(Model)
			if r.networkID != id {
				t.Fatal("overlapping network operation scheduled")
			}
		})
	}
}

func TestNetworkCloseCancelsQueuedCommand(t *testing.T) {
	m := networkModel(t)
	for i := 0; i < cap(m.sem); i++ {
		m.sem <- struct{}{}
	}
	cmd := m.startFetch(m.repos[0], false)
	m.Close()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if !errors.Is(msg.(fetchDoneMsg).err, context.Canceled) {
			t.Fatalf("want cancellation, got %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled queued command still waiting for a semaphore slot")
	}
}

func TestNetworkErrorRedactsCredentials(t *testing.T) {
	err := networkError(context.Background(), []string{"fetch"}, errors.New("failed"), "\x1b[31mfatal: https://user:secret@example.com/repo\x1b[0m\nerror")
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "\x1b") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("unsafe error: %s", err)
	}
}

func TestNetworkDetailsPreserveRunningOutput(t *testing.T) {
	m := networkModel(t)
	m.repos[0].networkErr = errors.New("failed")
	m.outputRunning = true
	m.outputLines = []string{"script output"}
	cancelled := false
	m.shellCancel = func() { cancelled = true }
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = mm.(Model)
	if cancelled || !m.outputRunning || len(m.outputLines) != 1 || m.outputLines[0] != "script output" {
		t.Fatal("viewing network details interrupted an active output producer")
	}
}

func TestNetworkCloseCancelsActiveFetch(t *testing.T) {
	m := networkModel(t)
	started := make(chan struct{}, 1)
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-stop:
		case <-r.Context().Done():
		}
	}))
	defer func() { close(stop); server.Close() }()
	r := m.repos[0]
	gitCmd(t, r.repo.Path, "remote", "add", "origin", server.URL)
	cmd := m.startFetch(r, false)
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("fetch did not reach remote")
	}
	m.Close()
	select {
	case msg := <-result:
		if !errors.Is(msg.(fetchDoneMsg).err, context.Canceled) || len(m.sem) != 0 {
			t.Fatalf("fetch not cancelled or semaphore leaked: %+v", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active fetch did not cancel")
	}
}

func TestNetworkLocalOnlyFetch(t *testing.T) {
	m := networkModel(t)
	r := m.repos[0]
	msg := m.startFetch(r, false)().(fetchDoneMsg)
	mm, _ := m.Update(msg)
	m = mm.(Model)
	if msg.err != nil || r.networkErr != nil || stripANSI(syncGlyph(r, false)) != "no-remote" {
		t.Fatalf("local-only repository treated as a failure: %+v", msg)
	}
}

func TestLoginDoesNotReplaySyncOrPush(t *testing.T) {
	for _, operation := range []string{"sync", "push"} {
		t.Run(operation, func(t *testing.T) {
			m := networkModel(t)
			r := m.repos[0]
			_, id := m.beginNetwork(r)
			err := errors.New("authentication failed")
			var msg tea.Msg = syncDoneMsg{path: r.repo.Path, id: id, err: err}
			if operation == "push" {
				msg = pushDoneMsg{path: r.repo.Path, id: id, err: err}
			}
			mm, _ := m.Update(msg)
			m = mm.(Model)
			if r.networkRunning || r.networkErr != err || r.fetchErr != nil {
				t.Fatal("write failure state incorrect")
			}
			other := m.repos[1]
			m.startFetch(other, true)
			mm, _ = m.Update(fetchDoneMsg{path: other.repo.Path, id: other.networkID, interactive: true})
			m = mm.(Model)
			if r.networkRunning || r.networkID != id || r.networkErr != err {
				t.Fatal("failed write automatically replayed")
			}
			mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
			m = mm.(Model)
			if !m.authRunning {
				t.Fatal("write failure cannot be resolved through explicit fetch login")
			}
		})
	}
}

// Real Git against an authenticated HTTP remote and a mock GCM-like helper:
// two silent failures, one interactive login, then concurrent cached fetches.
func TestNetworkCredentialHelperFlow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock credential helper uses POSIX sh")
	}
	dir := t.TempDir()
	bare := filepath.Join(dir, "origin.git")
	if err := os.Mkdir(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, bare, "init", "--bare", "-q", "-b", "master")
	seed := filepath.Join(dir, "seed")
	if err := os.Mkdir(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, seed, "init", "-q", "-b", "master")
	gitCmd(t, seed, "commit", "--allow-empty", "-qm", "seed")
	gitCmd(t, seed, "push", "-q", bare, "master")
	gitCmd(t, bare, "update-server-info")
	arrivals := make(chan struct{}, 2)
	release := make(chan struct{})
	files := http.FileServer(http.Dir(bare))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "test" || pass != "test" {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/info/refs") && r.Header.Get("X-Test-Parallel") == "yes" {
			arrivals <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		files.ServeHTTP(w, r)
	}))
	defer func() { close(release); server.Close() }()
	cache, prompts := filepath.Join(dir, "cache"), filepath.Join(dir, "prompts")
	helper := filepath.Join(dir, "helper.sh")
	if err := os.WriteFile(helper, []byte(`#!/bin/sh
test "$1" = get || exit 0
if ! test -f "$MANYGIT_TEST_CACHE"; then
    test "$GCM_INTERACTIVE" != 0 || exit 1
    printf 'login\n' >> "$MANYGIT_TEST_PROMPTS"
    touch "$MANYGIT_TEST_CACHE"
fi
printf 'username=test\npassword=test\n'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MANYGIT_TEST_CACHE", cache)
	t.Setenv("MANYGIT_TEST_PROMPTS", prompts)
	t.Setenv("GCM_INTERACTIVE", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	// Git's askpass fallback must also be suppressed after the helper declines.
	askpass := filepath.Join(dir, "askpass.sh")
	askpassLog := filepath.Join(dir, "askpass-called")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\ntouch \"$MANYGIT_TEST_ASKPASS_LOG\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MANYGIT_TEST_ASKPASS_LOG", askpassLog)
	t.Setenv("GIT_ASKPASS", askpass)
	t.Setenv("SSH_ASKPASS", askpass)
	var repos []discover.Repo
	for _, name := range []string{"alpha", "bravo"} {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		gitCmd(t, path, "init", "-q", "-b", "master")
		gitCmd(t, path, "remote", "add", "origin", server.URL)
		gitCmd(t, path, "config", "credential.helper", "")
		gitCmd(t, path, "config", "--add", "credential.helper", "!sh '"+helper+"'")
		repos = append(repos, discover.Repo{Name: name, Path: path})
	}
	m := New(config.Default(), dir, repos, nil)
	defer m.Close()
	for _, r := range m.repos {
		msg := m.startFetch(r, false)().(fetchDoneMsg)
		if msg.err == nil {
			t.Fatal("missing credentials should fail silently")
		}
		mm, _ := m.Update(msg)
		m = mm.(Model)
	}
	if _, err := os.Stat(prompts); !os.IsNotExist(err) {
		t.Fatal("background fetch prompted")
	}
	if _, err := os.Stat(askpassLog); !os.IsNotExist(err) {
		t.Fatal("background fetch invoked askpass")
	}
	a := m.repos[0]
	m.startFetch(a, true)
	c := &interactiveFetch{ctx: m.network.ctx, sem: m.sem, path: a.repo.Path, stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	mm, _ := m.Update(fetchDoneMsg{path: a.repo.Path, id: a.networkID, interactive: true})
	m = mm.(Model)
	// The scheduled retry for bravo is driven below, alongside a fresh alpha
	// fetch, to prove saved credentials work with actual parallel Git requests.
	b := m.repos[1]
	for _, r := range m.repos {
		gitCmd(t, r.repo.Path, "config", "http.extraHeader", "X-Test-Parallel: yes")
	}
	alpha := m.startFetch(a, false)
	bravo := networkCmd(m.network.ctx, m.sem, b.repo.Path, []string{"fetch", "--quiet"}, func(err error) tea.Msg { return fetchDoneMsg{path: b.repo.Path, id: b.networkID, err: err} })
	results := make(chan tea.Msg, 2)
	go func() { results <- alpha() }()
	go func() { results <- bravo() }()
	for i := 0; i < 2; i++ {
		select {
		case <-arrivals:
		case <-time.After(5 * time.Second):
			t.Fatal("cached fetches did not run concurrently")
		}
	}
	// Send a release for each request (the deferred close handles failure paths).
	release <- struct{}{}
	release <- struct{}{}
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			msg := result.(fetchDoneMsg)
			if msg.err != nil {
				t.Fatal(msg.err)
			}
			mm, _ := m.Update(msg)
			m = mm.(Model)
		case <-time.After(5 * time.Second):
			t.Fatal("cached fetch did not finish")
		}
	}
	data, err := os.ReadFile(prompts)
	if err != nil || string(data) != "login\n" {
		t.Fatalf("want one login, got %q, %v", data, err)
	}
	if a.networkErr != nil || b.networkErr != nil {
		t.Fatal("successful fetch did not clear errors")
	}
	// A public HTTP remote needs no credentials, even with a helper configured.
	public := httptest.NewServer(files)
	defer public.Close()
	gitCmd(t, a.repo.Path, "remote", "set-url", "origin", public.URL)
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	if msg := m.startFetch(a, false)().(fetchDoneMsg); msg.err != nil {
		t.Fatalf("public fetch failed: %v", msg.err)
	}
	data, err = os.ReadFile(prompts)
	if err != nil || string(data) != "login\n" {
		t.Fatal("public fetch invoked credential helper login")
	}
}
