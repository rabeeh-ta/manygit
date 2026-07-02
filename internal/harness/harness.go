// Package harness detects and drives the AI coding CLIs (Claude Code, Codex)
// that manygit can use for its AI features. manygit shells out to whichever CLI
// is installed, using the CLI's own auth — no API keys of its own.
package harness

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// Harness is a supported AI CLI.
type Harness struct {
	Name string   // config value + display name, e.g. "claude"
	Bin  string   // executable looked up on PATH
	args []string // one-shot (non-interactive) invocation; the prompt is appended
}

// All is the set of harnesses manygit knows how to drive, in display order. Each
// runs with the CLI's own default model/settings (no model override).
var All = []Harness{
	{Name: "claude", Bin: "claude", args: []string{"-p"}},
	{Name: "codex", Bin: "codex", args: []string{"exec"}},
}

// Installed reports whether the harness's binary is on PATH.
func (h Harness) Installed() bool {
	_, err := exec.LookPath(h.Bin)
	return err == nil
}

// ByName returns the named harness (ok=false if unknown).
func ByName(name string) (Harness, bool) {
	for _, h := range All {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}

// FirstInstalled returns the name of the first installed harness, or "".
func FirstInstalled() string {
	for _, h := range All {
		if h.Installed() {
			return h.Name
		}
	}
	return ""
}

// Available reports whether the named harness is known and installed.
func Available(name string) bool {
	h, ok := ByName(name)
	return ok && h.Installed()
}

// oneShotArgs is the full argv (minus the binary) for a one-shot run: the base
// one-shot flags, then the prompt (kept last as the positional arg).
func (h Harness) oneShotArgs(prompt string) []string {
	return append(append([]string{}, h.args...), prompt)
}

// OneShot runs prompt through the harness non-interactively in dir and returns
// its stdout, using the CLI's default model/settings. This makes a real AI call
// using the CLI's auth.
func (h Harness) OneShot(ctx context.Context, dir, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, h.Bin, h.oneShotArgs(prompt)...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return "", &RunError{Harness: h.Name, Err: err, Stderr: msg}
	}
	return strings.TrimSpace(out.String()), nil
}
