package selfupdate

import "testing"

// aggregate is the pure core of DownloadStats — pulled out so it's testable
// without a network call. This test guards the two things easy to get wrong:
// checksums.txt must be excluded (so OS split == total), and the OS bucketing.
func TestAggregate(t *testing.T) {
	rs := []Release{
		{Tag: "v1.1.0", PublishedAt: "2026-08-01T00:00:00Z", Assets: []Asset{
			{Name: "manygit_linux_amd64.tar.gz", DownloadCount: 5},
			{Name: "manygit_darwin_arm64.tar.gz", DownloadCount: 3},
			{Name: "checksums.txt", DownloadCount: 9}, // must NOT count
		}},
		{Tag: "v1.0.9", PublishedAt: "2026-07-18T00:00:00Z", Assets: []Asset{
			{Name: "manygit_linux_arm64.tar.gz", DownloadCount: 2},
		}},
	}
	s := aggregate(rs, 10)

	if s.TotalReleases != 2 {
		t.Errorf("TotalReleases = %d, want 2", s.TotalReleases)
	}
	if s.BinaryDownloads != 10 { // 5+3+2, checksums excluded
		t.Errorf("BinaryDownloads = %d, want 10 (checksums must not count)", s.BinaryDownloads)
	}
	if s.ByOS["linux"] != 7 || s.ByOS["darwin"] != 3 {
		t.Errorf("ByOS = %v, want linux 7 darwin 3", s.ByOS)
	}
	if s.ByOS["linux"]+s.ByOS["darwin"] != s.BinaryDownloads {
		t.Error("OS split must add up to the binary total")
	}
	if len(s.Recent) != 2 || s.Recent[0].Tag != "v1.1.0" || s.Recent[0].Downloads != 8 {
		t.Errorf("Recent[0] = %+v, want v1.1.0 with 8 downloads", s.Recent[0])
	}
	if s.Recent[0].Date != "2026-08-01" {
		t.Errorf("date = %q, want 2026-08-01", s.Recent[0].Date)
	}

	// recent cap
	if got := aggregate(rs, 1); len(got.Recent) != 1 {
		t.Errorf("recent=1 gave %d rows", len(got.Recent))
	}
}
