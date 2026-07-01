package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault_HasSaneValues(t *testing.T) {
	c := Default()
	if c.MaxDepth != 3 {
		t.Errorf("MaxDepth = %d, want 3", c.MaxDepth)
	}
	if c.Concurrency != 8 {
		t.Errorf("Concurrency = %d, want 8", c.Concurrency)
	}
	if c.OpenCmd != "code" {
		t.Errorf("OpenCmd = %q, want code", c.OpenCmd)
	}
	if !c.PruneSet()["node_modules"] {
		t.Errorf("default prune should include node_modules")
	}
}

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.yml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MaxDepth != 3 {
		t.Errorf("MaxDepth = %d, want default 3", c.MaxDepth)
	}
}

func TestLoad_OverridesFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	body := "max_depth: 5\nconcurrency: 2\nopen_cmd: lazygit\nprune:\n  - foo\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MaxDepth != 5 || c.Concurrency != 2 || c.OpenCmd != "lazygit" {
		t.Errorf("overrides not applied: %+v", c)
	}
	set := c.PruneSet()
	if !set["foo"] || !set["node_modules"] {
		t.Errorf("prune should include both foo and node_modules: %v", set)
	}
}
