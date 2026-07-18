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
	flag.Usage = func() {
		w := flag.CommandLine.Output()
		fmt.Fprint(w, `manygit — a lazygit-style TUI for a whole tree of git repos

Usage:
  manygit                 launch the TUI, scanning the current directory
  manygit --root <dir>    launch scanning a specific folder
  manygit stats           print public download counts (no auth, no telemetry)

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprint(w, "\nMore: https://github.com/rabeeh-ta/manygit\n")
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("manygit", version)
		return
	}

	// `manygit stats` — public download counts from GitHub, no auth, no telemetry.
	// Anyone can run it; it only reads aggregate numbers GitHub already keeps.
	if flag.Arg(0) == "stats" {
		printStats()
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
		// Tell the re-exec'd (new) binary it arrived via our updater, and from
		// which version. This is the ONLY thing that sets the var, so a fresh
		// install or `go install` never triggers the changelog — see
		// internal/tui changelog handling. current is the OLD version (this
		// process was built before the update).
		env := append(os.Environ(), tui.EnvUpdatedFrom+"="+current)
		err = syscall.Exec(exe, os.Args, env)
	}
	if err != nil {
		fmt.Println("Please restart manygit to use the new version.")
		os.Exit(0)
	}
}

// printStats fetches the public GitHub download counts and prints a small table:
// total releases, all-time binary downloads split by OS, and the last 10 tags.
func printStats() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := selfupdate.DownloadStats(ctx, 10)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stats:", err)
		os.Exit(1)
	}
	fmt.Printf("manygit — public download stats\n\n")
	fmt.Printf("  total releases       %d\n", s.TotalReleases)
	fmt.Printf("  all-time downloads   %d   (linux %d · darwin %d)\n\n",
		s.BinaryDownloads, s.ByOS["linux"], s.ByOS["darwin"])
	fmt.Printf("  last %d releases\n", len(s.Recent))
	for _, r := range s.Recent {
		fmt.Printf("    %-9s %s   %5d\n", r.Tag, r.Date, r.Downloads)
	}
	fmt.Printf("\n  counts are binary (.tar.gz) downloads GitHub keeps per release;\n")
	fmt.Printf("  installs and self-updates both count. no telemetry — public data.\n")
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
