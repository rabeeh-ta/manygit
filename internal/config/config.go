// Package config loads manygit settings with baked-in defaults. The config
// file is optional and lives at $XDG_CONFIG_HOME/manygit/config.yml (or, with
// no XDG_CONFIG_HOME set, %AppData%\manygit\config.yml on Windows).
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/rabeeh-ta/manygit/internal/discover"
	"github.com/rabeeh-ta/manygit/internal/xdgdir"
)

// Config is the effective configuration.
type Config struct {
	Root         string   `yaml:"root"`
	MaxDepth     int      `yaml:"max_depth"`
	Concurrency  int      `yaml:"concurrency"`
	OpenCmd      string   `yaml:"open_cmd"`
	Prune        []string `yaml:"prune"`         // merged with defaults
	StatusGlyphs string   `yaml:"status_glyphs"` // "unicode" (↑↓) or "ascii" (+-)
	Theme        string   `yaml:"theme"`         // color theme name (see the tui theme list)
	Harness      string   `yaml:"harness"`       // AI harness: "claude" or "codex" (see internal/harness)
	NewsDays     int      `yaml:"news_days"`     // top-bar news feed window in days (commits newer than this)
	Mouse        string   `yaml:"mouse"`         // "on" (click + wheel) or "off" (the terminal keeps the mouse)
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{MaxDepth: 3, Concurrency: 8, OpenCmd: "code", StatusGlyphs: "unicode", Theme: "default", NewsDays: 3, Mouse: "on"}
}

// MouseEnabled reports whether manygit captures the mouse (click to focus and
// select, wheel to move). A string rather than a bool for the same reason as
// StatusGlyphs: an absent key must mean the default, and a YAML false can't be
// told apart from absent. mouse: off hands the mouse back to the terminal, so
// plain drag-to-select works without holding shift.
func (c Config) MouseEnabled() bool {
	return c.Mouse != "off"
}

// UnicodeGlyphs reports whether ahead/behind should use ↑/↓ (true) or the
// alignment-safe ASCII +/- (set status_glyphs: ascii to force ASCII).
func (c Config) UnicodeGlyphs() bool {
	return c.StatusGlyphs != "ascii"
}

// ConfigPath returns the config file path (see internal/xdgdir).
func ConfigPath() string {
	return filepath.Join(xdgdir.ConfigHome(), "manygit", "config.yml")
}

// Load reads config from path (empty = ConfigPath()). A missing file yields
// defaults with no error. File values override defaults; Prune entries merge.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = ConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	var file Config
	if err := yaml.Unmarshal(data, &file); err != nil {
		return cfg, err
	}
	if file.Root != "" {
		cfg.Root = file.Root
	}
	if file.MaxDepth != 0 {
		cfg.MaxDepth = file.MaxDepth
	}
	if file.Concurrency != 0 {
		cfg.Concurrency = file.Concurrency
	}
	if file.OpenCmd != "" {
		cfg.OpenCmd = file.OpenCmd
	}
	if file.StatusGlyphs != "" {
		cfg.StatusGlyphs = file.StatusGlyphs
	}
	if file.Theme != "" {
		cfg.Theme = file.Theme
	}
	if file.Harness != "" {
		cfg.Harness = file.Harness
	}
	if file.NewsDays != 0 {
		cfg.NewsDays = file.NewsDays
	}
	if file.Mouse != "" {
		cfg.Mouse = file.Mouse
	}
	cfg.Prune = append(cfg.Prune, file.Prune...)
	return cfg, nil
}

// Save writes cfg to path (empty = ConfigPath()), creating the directory. Used
// when the settings screen changes a runtime setting (theme, glyphs, editor).
func Save(cfg Config, path string) error {
	if path == "" {
		path = ConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// PruneSet is the default prune set merged with any user-configured names.
func (c Config) PruneSet() map[string]bool {
	set := discover.DefaultPrune()
	for _, n := range c.Prune {
		set[n] = true
	}
	return set
}
