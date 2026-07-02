// Package selfupdate checks GitHub Releases for a newer manygit and, on request,
// downloads the matching binary and swaps it in place. It uses the public
// releases API with no auth (manygit's repo is public) and fails soft: any
// network or parse error just means "no update", never a crash.
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const repo = "rabeeh-ta/manygit"

// maxDownload caps a release asset read so a bad/huge response can't exhaust
// memory (the binary is a few MB; 100 MB is comfortably above any real build).
const maxDownload = 100 << 20

// Release is the subset of the GitHub release payload we use.
type Release struct {
	Tag    string  `json:"tag_name"`
	Assets []Asset `json:"assets"`
}

// Asset is one uploaded release file.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Latest fetches the newest published release. ctx should carry a short timeout.
func Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github api: %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// assetName is the archive name goreleaser produces for an os/arch pair, e.g.
// "manygit_darwin_arm64.tar.gz".
func assetName(goos, goarch string) string {
	return fmt.Sprintf("manygit_%s_%s.tar.gz", goos, goarch)
}

// Apply downloads this platform's binary from r, verifies it against the
// release checksums, and atomically replaces the running executable. The caller
// re-execs afterwards. It needs write access to the install dir (true for the
// recommended ~/.local/bin; /usr/local/bin would need sudo).
func Apply(ctx context.Context, r Release) error {
	want := assetName(runtime.GOOS, runtime.GOARCH)
	var tarURL, sumURL string
	for _, a := range r.Assets {
		switch a.Name {
		case want:
			tarURL = a.URL
		case "checksums.txt":
			sumURL = a.URL
		}
	}
	if tarURL == "" {
		return fmt.Errorf("release %s has no binary for %s/%s", r.Tag, runtime.GOOS, runtime.GOARCH)
	}

	tarData, err := download(ctx, tarURL)
	if err != nil {
		return err
	}
	if sumURL != "" {
		sums, err := download(ctx, sumURL)
		if err != nil {
			return err
		}
		if exp := checksumFor(string(sums), want); exp != "" {
			got := sha256.Sum256(tarData)
			if exp != hex.EncodeToString(got[:]) {
				return fmt.Errorf("checksum mismatch for %s", want)
			}
		}
	}

	bin, err := extractBinary(tarData, "manygit")
	if err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".manygit-new-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (self-update needs write access there): %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, exe); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

// checksumFor returns the hex sha256 for name from a goreleaser checksums.txt
// ("<sha256>  <filename>" per line), or "" if not listed.
func checksumFor(sums, name string) string {
	for _, ln := range strings.Split(sums, "\n") {
		f := strings.Fields(ln)
		if len(f) == 2 && f[1] == name {
			return f[0]
		}
	}
	return ""
}

// extractBinary pulls the named file out of a .tar.gz blob.
func extractBinary(targz []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(targz))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == name && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

// IsRelease reports whether v is a clean released version (so the update check
// should run). A local "0.1.0-dev" build, or anything unparseable, returns false
// — dev builds don't nag about updates.
func IsRelease(v string) bool {
	_, pre, ok := parse(v)
	return ok && pre == ""
}

// NewerThan reports whether release tag `latest` is a strictly newer version
// than `current`. Unparseable inputs sort as older.
func NewerThan(latest, current string) bool {
	return cmp(latest, current) > 0
}

func cmp(a, b string) int {
	an, _, aok := parse(a)
	bn, _, bok := parse(b)
	if !aok || !bok {
		switch {
		case aok:
			return 1
		case bok:
			return -1
		default:
			return 0
		}
	}
	for i := range an {
		if an[i] != bn[i] {
			if an[i] > bn[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

// parse reads a "vMAJOR.MINOR.PATCH[-pre]" string into its three numbers and the
// prerelease tag. ok is false if the numeric core doesn't parse.
func parse(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || parts[0] == "" {
		return nums, pre, false
	}
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return nums, pre, false
		}
		nums[i] = n
	}
	return nums, pre, true
}
