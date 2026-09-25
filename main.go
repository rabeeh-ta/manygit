package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rabeeh-ta/manygit/internal/config"
	"github.com/rabeeh-ta/manygit/internal/discover"
	"github.com/rabeeh-ta/manygit/internal/harness"
	"github.com/rabeeh-ta/manygit/internal/selfupdate"
	"github.com/rabeeh-ta/manygit/internal/tui"
)

var version = "0.1.0-dev"

// ldflagVersion preserves what the linker stamped into version, captured before
// ResolveVersion may replace it with the module version. installOwner needs the
// original: a stamped ldflag is what tells a release build apart from a
// `go install` one, and resolving first would erase that distinction.
var ldflagVersion = version

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

	// Windows can't overwrite its own running executable, so a self-update there
	// leaves the replaced binary behind as "<exe>.old" (see selfupdate.Apply).
	// The lock on it clears once that old process exits, so pick it up now, on
	// the next launch. A no-op everywhere else: nothing ever creates this file.
	selfupdate.CleanupStale()

	// GoReleaser stamps the version into the ldflag; `go install` leaves it at the
	// source default and puts the version in build info. Resolve before any read.
	version = selfupdate.ResolveVersion(version, mainModuleVersion())

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
	var updateNotice string
	if !*noUpdate && os.Getenv("MANYGIT_NO_UPDATE_CHECK") == "" && selfupdate.IsRelease(version) {
		updateNotice = maybeSelfUpdate(version)
	}

	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		showNotice(updateNotice)
		os.Exit(1)
	}

	if cfg.Harness == "" {
		cfg.Harness = harness.FirstInstalled() // "" if neither claude nor codex is on PATH
	}

	scanRoot := resolveRoot(*root, cfg.Root)
	repos, err := discover.Discover(scanRoot, discover.Options{MaxDepth: cfg.MaxDepth, Prune: cfg.PruneSet()})
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover:", err)
		showNotice(updateNotice)
		os.Exit(1)
	}
	if len(repos) == 0 {
		fmt.Fprintf(os.Stderr, "no git repositories found under %s (max depth %d)\n", scanRoot, cfg.MaxDepth)
		showNotice(updateNotice)
		os.Exit(1)
	}

	// *.sh scripts near the root (root-level + one dir deep, e.g. scripts/).
	scripts := discover.Scripts(scanRoot, 2, cfg.PruneSet())

	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithReportFocus()}
	if cfg.MouseEnabled() {
		// Cell motion, not all motion: clicks and the wheel are all we read, and
		// all-motion sends an event for every cell the pointer crosses.
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(tui.New(cfg, scanRoot, repos, scripts), opts...)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		showNotice(updateNotice)
		os.Exit(1)
	}

	showNotice(updateNotice)
}

// showNotice prints a held-back update notice and records that it was shown.
//
// It is called on every exit path after the check runs, not just the successful
// one: the notice is computed before the config and repo scan, so a config error
// or an empty directory would otherwise swallow it. It is deliberately not
// printed at the point it is computed — the TUI starts with WithAltScreen, which
// clears the terminal and would wipe anything written beforehand.
func showNotice(notice string) {
	if notice == "" {
		return
	}
	fmt.Println(notice)
	selfupdate.MarkNotified(time.Now())
}

// noticeInterval is how often a managed install is reminded that a newer release
// exists. It cannot act on the notice from in here, so a line every launch would
// just become wallpaper.
const noticeInterval = 24 * time.Hour

// installOwner reports which package manager owns this binary, or SelfManaged.
// The path is resolved through symlinks first because Homebrew links its bin
// entry to the real file staged under the Caskroom.
func installOwner() string {
	exe, err := os.Executable()
	if err != nil {
		return selfupdate.SelfManaged
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return selfupdate.ManagedBy(exe, ldflagVersion, mainModuleVersion())
}

// mainModuleVersion is the version the Go toolchain recorded for the main module:
// a real semver for `go install <pkg>@<version>`, "(devel)" for a plain go build.
func mainModuleVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return bi.Main.Version
}

// maybeSelfUpdate checks for a newer release. A self-managed install is offered
// the update and, if the user agrees, has its binary replaced and re-execs into
// it — that runs before the TUI so it can prompt on the plain terminal. A managed
// install is never modified; it returns the notice to print once the TUI exits.
// Any failure is reported and then ignored (launch old).
func maybeSelfUpdate(current string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := selfupdate.Latest(ctx)
	if err != nil || r.Tag == "" || !selfupdate.NewerThan(r.Tag, current) {
		return "" // offline, no release, or already current — say nothing
	}

	// A managed install's binary belongs to a package manager. Name the command
	// that updates it and never touch the file.
	if owner := installOwner(); owner != selfupdate.SelfManaged {
		notice := selfupdate.UpdateNotice(owner, r.Tag, current)
		if notice == "" || !selfupdate.ShouldNotify(selfupdate.LastNotified(), time.Now(), noticeInterval) {
			return ""
		}
		return notice
	}

	fmt.Printf("manygit %s is available (you have %s).\nUpdate now? [y/N] ", r.Tag, current)
	var ans string
	fmt.Scanln(&ans)
	if strings.ToLower(strings.TrimSpace(ans)) != "y" {
		return ""
	}

	fmt.Println("Updating...")
	dctx, dcancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer dcancel()
	if err := selfupdate.Apply(dctx, r); err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\ncontinuing on %s\n", err, current)
		return ""
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
		err = relaunch(exe, os.Args, env)
	}
	if err != nil {
		fmt.Println("Please restart manygit to use the new version.")
		os.Exit(0)
	}
	return ""
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
	fmt.Printf("  all-time downloads   %d   (linux %d · darwin %d · windows %d)\n\n",
		s.BinaryDownloads, s.ByOS["linux"], s.ByOS["darwin"], s.ByOS["windows"])
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
