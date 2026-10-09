package git

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestNetworkCommandPromptEnvironment(t *testing.T) {
	t.Setenv("GCM_INTERACTIVE", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("GIT_ASKPASS", "custom-askpass")
	t.Setenv("SSH_ASKPASS", "custom-ssh-askpass")
	t.Setenv("GIT_SSH_COMMAND", "ssh -F custom-config")
	for _, interactive := range []bool{false, true} {
		cmd := NetworkCommand(context.Background(), t.TempDir(), interactive, "fetch", "--quiet")
		env := make(map[string]string)
		for _, entry := range cmd.Environ() {
			key, value, _ := strings.Cut(entry, "=")
			env[key] = value
		}
		for _, key := range []string{"GCM_INTERACTIVE", "GIT_TERMINAL_PROMPT", "GIT_ASKPASS", "SSH_ASKPASS", "GIT_SSH_COMMAND"} {
			want := os.Getenv(key)
			if !interactive {
				switch key {
				case "GCM_INTERACTIVE", "GIT_TERMINAL_PROMPT":
					want = "0"
				case "GIT_ASKPASS":
					want = ""
				}
			}
			if env[key] != want {
				t.Errorf("interactive=%v, %s=%q, want %q", interactive, key, env[key], want)
			}
		}
	}
	if os.Getenv("GCM_INTERACTIVE") != "1" || os.Getenv("GIT_ASKPASS") != "custom-askpass" {
		t.Fatal("command changed parent environment")
	}
}

func TestInteractiveNetworkRespectsUserPromptRestrictions(t *testing.T) {
	t.Setenv("GCM_INTERACTIVE", "0")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	cmd := NetworkCommand(context.Background(), t.TempDir(), true, "fetch")
	for _, entry := range cmd.Environ() {
		if entry == "GCM_INTERACTIVE=1" || entry == "GIT_TERMINAL_PROMPT=1" {
			t.Fatal("interactive retry overrode user's prompt restrictions")
		}
	}
}
