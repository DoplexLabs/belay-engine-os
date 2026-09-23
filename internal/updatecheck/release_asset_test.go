package updatecheck

import "testing"

func TestReleaseAssetSuffixByPlatform(t *testing.T) {
	for _, test := range []struct {
		goos, goarch, want string
	}{
		{"darwin", "arm64", "-darwin-arm64.tar.gz"},
		{"darwin", "amd64", "-darwin-arm64.tar.gz"},
		{"linux", "amd64", "-darwin-arm64.tar.gz"},
		{"windows", "amd64", "-windows-amd64.zip"},
		{"windows", "arm64", "-windows-arm64.zip"},
	} {
		if got := releaseAssetSuffix(test.goos, test.goarch); got != test.want {
			t.Errorf("releaseAssetSuffix(%s, %s) = %q, want %q", test.goos, test.goarch, got, test.want)
		}
	}
	assets := []asset{{Name: "belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip"}}
	if !hasReleaseAsset(assets, releaseAssetSuffix("windows", "amd64")) {
		t.Error("windows amd64 zip not recognized")
	}
	if hasReleaseAsset(assets, releaseAssetSuffix("windows", "arm64")) {
		t.Error("windows arm64 matched an amd64 zip")
	}
	if hasReleaseAsset(assets, releaseAssetSuffix("darwin", "arm64")) {
		t.Error("darwin matched a windows zip")
	}
}
