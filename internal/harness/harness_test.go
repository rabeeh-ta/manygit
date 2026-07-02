package harness

import "testing"

func TestByName(t *testing.T) {
	if h, ok := ByName("claude"); !ok || h.Bin != "claude" {
		t.Errorf("ByName(claude) = %+v, %v", h, ok)
	}
	if _, ok := ByName("nope"); ok {
		t.Error("ByName(nope) should be !ok")
	}
}

func TestInstalledDetection(t *testing.T) {
	// A harness that maps to a binary that is never on PATH must report not
	// installed; Available for it must be false. (We can't assume claude/codex
	// exist in CI, so only assert the negative case here.)
	fake := Harness{Name: "ghost", Bin: "manygit-nonexistent-binary-xyz"}
	if fake.Installed() {
		t.Error("a nonexistent binary should not be reported installed")
	}
	if Available("ghost") {
		t.Error("Available for an unknown harness should be false")
	}
	// FirstInstalled returns either "" or a name that is actually installed.
	if n := FirstInstalled(); n != "" && !Available(n) {
		t.Errorf("FirstInstalled returned %q which is not Available", n)
	}
}
