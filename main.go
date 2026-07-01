package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/config"
	"manygit/internal/discover"
	"manygit/internal/tui"
)

var version = "0.1.0-dev"

func main() {
	root := flag.String("root", "", "directory to scan for repos (default: $MANYGIT_ROOT or cwd)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("manygit", version)
		return
	}

	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
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

	p := tea.NewProgram(tui.New(cfg, repos), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
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
