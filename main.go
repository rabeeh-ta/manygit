package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/config"
	"manygit/internal/discover"
	"manygit/internal/harness"
	"manygit/internal/selfupdate"
	"manygit/internal/tui"
)

var version = "0.1.0-dev"

func main() {
	root := flag.String("root", "", "directory to scan for repos (default: $MANYGIT_ROOT or cwd)")
	showVersion := flag.Bool("version", false, "print version and exit")
	noUpdate := flag.Bool("no-update-check", false, "skip the check for a newer release on launch")
	flag.Parse()

	if *showVersion {
		fmt.Println("manygit", version)
		return
	}

	// Offer an update before taking over the screen. Skipped for dev builds and
	// when disabled; silent on any network/API hiccup.
	if !*noUpdate && os.Getenv("MANYGIT_NO_UPDATE_CHECK") == "" && selfupdate.IsRelease(version) {
		maybeSelfUpdate(version)
	}

	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	if cfg.Harness == "" {
		cfg.Harness = harness.FirstInstalled() // "" if neither claude nor codex is on PATH
	}

	scanRoot := resolveRoot(*root, cfg.Root)
	repos, err := discover.Discover(scanRoot, discover.Options{MaxDepth: cfg.MaxDepth, Prune: cfg.PruneSet()})
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover:", err)
		os.Exit(1)
	}
	if len(repos) == 0 {
		fmt.Fprintf(os.Stderr, "no git repositories found under %s (max depth %d)\n", scanRoot, cfg.MaxDepth)
		os.Exit(1)
	}

	// *.sh scripts near the root (root-level + one dir deep, e.g. scripts/).
	scripts := discover.Scripts(scanRoot, 2, cfg.PruneSet())

	p := tea.NewProgram(tui.New(cfg, scanRoot, repos, scripts), tea.WithAltScreen(), tea.WithReportFocus())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// maybeSelfUpdate checks for a newer release and, if the user agrees, replaces
// the running binary and re-execs into it. Runs before the TUI so it can prompt
// on the plain terminal. Any failure is reported and then ignored (launch old).
func maybeSelfUpdate(current string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := selfupdate.Latest(ctx)
	if err != nil || r.Tag == "" || !selfupdate.NewerThan(r.Tag, current) {
		return // offline, no release, or already current — say nothing
	}

	fmt.Printf("manygit %s is available (you have %s).\nUpdate now? [y/N] ", r.Tag, current)
	var ans string
	fmt.Scanln(&ans)
	if strings.ToLower(strings.TrimSpace(ans)) != "y" {
		return
	}

	fmt.Println("Updating...")
	dctx, dcancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer dcancel()
	if err := selfupdate.Apply(dctx, r); err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\ncontinuing on %s\n", err, current)
		return
	}

	fmt.Printf("Updated to %s — relaunching...\n", r.Tag)
	exe, err := os.Executable()
	if err == nil {
		err = syscall.Exec(exe, os.Args, os.Environ())
	}
	if err != nil {
		fmt.Println("Please restart manygit to use the new version.")
		os.Exit(0)
	}
}

func resolveRoot(flagRoot, cfgRoot string) string {
	if flagRoot != "" {
		return flagRoot
	}
	if env := os.Getenv("MANYGIT_ROOT"); env != "" {
		return env
	}
	if cfgRoot != "" {
		return cfgRoot
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
