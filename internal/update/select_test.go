package update

import "testing"

func TestSelectBinaryAssetSkipsSidecars(t *testing.T) {
	// A real VayuPress release: one binary plus checksum and cosign bundle.
	assets := []Asset{
		{Name: "vayupress"},
		{Name: "vayupress.sha256"},
		{Name: "vayupress.cosign.bundle"},
	}
	got := selectBinaryAsset(assets, "linux", "amd64")
	if got == nil || got.Name != "vayupress" {
		t.Fatalf("expected the bare binary, got %+v", got)
	}
}

func TestSelectBinaryAssetCosignBundleNeverChosen(t *testing.T) {
	// Even if the bundle is listed first, it must never be picked as the binary.
	assets := []Asset{
		{Name: "vayupress.cosign.bundle"},
		{Name: "vayupress.sha256"},
		{Name: "vayupress"},
	}
	got := selectBinaryAsset(assets, "linux", "amd64")
	if got == nil || got.Name != "vayupress" {
		t.Fatalf("expected the bare binary, got %+v", got)
	}
}

func TestSelectBinaryAssetMatchesPlatform(t *testing.T) {
	assets := []Asset{
		{Name: "vayupress_darwin_arm64"},
		{Name: "vayupress_linux_amd64"},
		{Name: "vayupress_linux_arm64"},
		{Name: "vayupress_windows_amd64.exe"},
		{Name: "checksums.txt"},
	}
	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "vayupress_linux_amd64"},
		{"linux", "arm64", "vayupress_linux_arm64"},
		{"darwin", "arm64", "vayupress_darwin_arm64"},
		{"windows", "amd64", "vayupress_windows_amd64.exe"},
	}
	for _, c := range cases {
		got := selectBinaryAsset(assets, c.goos, c.goarch)
		if got == nil || got.Name != c.want {
			t.Errorf("%s/%s: want %q, got %+v", c.goos, c.goarch, c.want, got)
		}
	}
}

func TestSelectBinaryAssetArchAliases(t *testing.T) {
	assets := []Asset{
		{Name: "app-linux-x86_64.tar.gz"},
		{Name: "app-linux-aarch64.tar.gz"},
	}
	if got := selectBinaryAsset(assets, "linux", "amd64"); got == nil || got.Name != "app-linux-x86_64.tar.gz" {
		t.Errorf("amd64 should match x86_64, got %+v", got)
	}
	if got := selectBinaryAsset(assets, "linux", "arm64"); got == nil || got.Name != "app-linux-aarch64.tar.gz" {
		t.Errorf("arm64 should match aarch64, got %+v", got)
	}
}

func TestSelectBinaryAssetNoBinary(t *testing.T) {
	assets := []Asset{
		{Name: "vayupress.sha256"},
		{Name: "vayupress.sig"},
	}
	if got := selectBinaryAsset(assets, "linux", "amd64"); got != nil {
		t.Fatalf("expected nil when only sidecars are present, got %+v", got)
	}
}

func TestSelectChecksumAssetExactSibling(t *testing.T) {
	assets := []Asset{
		{Name: "vayupress_linux_amd64"},
		{Name: "vayupress_linux_amd64.sha256"},
		{Name: "vayupress_linux_arm64"},
		{Name: "vayupress_linux_arm64.sha256"},
	}
	got := selectChecksumAsset(assets, "vayupress_linux_amd64")
	if got == nil || got.Name != "vayupress_linux_amd64.sha256" {
		t.Fatalf("expected the amd64 checksum, got %+v", got)
	}
}

func TestSelectChecksumAssetSoleFallback(t *testing.T) {
	// VayuPress ships "vayupress" + "vayupress.sha256" (not "vayupress.sha256"
	// keyed to a longer binary name), so the sole-asset fallback must apply.
	assets := []Asset{
		{Name: "vayupress.tar.gz"},
		{Name: "vayupress.sha256"},
	}
	got := selectChecksumAsset(assets, "vayupress.tar.gz")
	if got == nil || got.Name != "vayupress.sha256" {
		t.Fatalf("expected sole .sha256 fallback, got %+v", got)
	}
}

func TestSelectChecksumAssetAmbiguousIsNil(t *testing.T) {
	// Multiple checksums and no exact sibling → refuse rather than guess.
	assets := []Asset{
		{Name: "a.sha256"},
		{Name: "b.sha256"},
	}
	if got := selectChecksumAsset(assets, "vayupress"); got != nil {
		t.Fatalf("expected nil for ambiguous checksums, got %+v", got)
	}
}

func TestIsMetadataAsset(t *testing.T) {
	meta := []string{"x.sha256", "x.sig", "x.cosign.bundle", "sbom.spdx.json", "notes.md", "x.asc"}
	for _, n := range meta {
		if !isMetadataAsset(n) {
			t.Errorf("%q should be metadata", n)
		}
	}
	bins := []string{"vayupress", "vayupress_linux_amd64", "app.tar.gz", "tool.exe"}
	for _, n := range bins {
		if isMetadataAsset(n) {
			t.Errorf("%q should NOT be metadata", n)
		}
	}
}
