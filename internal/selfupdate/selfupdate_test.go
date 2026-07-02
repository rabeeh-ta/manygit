package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestNewerThan(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.1", "v0.1.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"0.2.0", "v0.1.0", true},     // mixed v-prefix
		{"v0.2.0", "0.1.0-dev", true}, // dev current is older than any release
		{"garbage", "v0.1.0", false},  // unparseable latest never wins
	}
	for _, c := range cases {
		if got := NewerThan(c.latest, c.current); got != c.want {
			t.Errorf("NewerThan(%q,%q)=%v want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.1.0":     true,
		"0.1.0":      true,
		"0.1.0-dev":  false,
		"v1.2.3-rc1": false,
		"dev":        false,
		"":           false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q)=%v want %v", v, got, want)
		}
	}
}

func TestAssetName(t *testing.T) {
	if got := assetName("darwin", "arm64"); got != "manygit_darwin_arm64.tar.gz" {
		t.Errorf("assetName = %q", got)
	}
	if got := assetName("linux", "amd64"); got != "manygit_linux_amd64.tar.gz" {
		t.Errorf("assetName = %q", got)
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "abc123  manygit_linux_amd64.tar.gz\ndef456  manygit_darwin_arm64.tar.gz\n"
	if got := checksumFor(sums, "manygit_darwin_arm64.tar.gz"); got != "def456" {
		t.Errorf("checksumFor = %q", got)
	}
	if got := checksumFor(sums, "nope.tar.gz"); got != "" {
		t.Errorf("checksumFor(missing) = %q, want empty", got)
	}
}

func TestExtractBinary(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/echo fake-binary\n")
	// include a decoy file plus the real binary
	for _, f := range []struct {
		name string
		data []byte
	}{{"README.md", []byte("readme")}, {"manygit", payload}} {
		_ = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(f.data)
	}
	tw.Close()
	gz.Close()

	got, err := extractBinary(buf.Bytes(), "manygit")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("extracted %q, want %q", got, payload)
	}
	if _, err := extractBinary(buf.Bytes(), "missing"); err == nil {
		t.Error("extractBinary(missing) should error")
	}
}
