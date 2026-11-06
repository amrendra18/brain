package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageManagerFiles(t *testing.T) {
	output := t.TempDir()
	var checksums strings.Builder
	for i, target := range Targets {
		pair := strings.Split(target, "/")
		suffix := ".tar.gz"
		if pair[0] == "windows" {
			suffix = ".zip"
		}
		fmt.Fprintf(&checksums, "%064x  brain_0.1.0-dev_%s_%s%s\n", i+1, pair[0], pair[1], suffix)
	}
	if err := os.WriteFile(filepath.Join(output, "brain_0.1.0-dev_checksums.txt"), []byte(checksums.String()), 0600); err != nil {
		t.Fatal(err)
	}
	releaseURL := "https://github.com/brain-fixture/owned-fixture/releases/download/v0.1.0-dev"
	if err := Manifests(output, releaseURL, "MIT", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "Formula/brain.rb"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "sha256 ") != 4 || strings.Count(string(data), "url ") != 4 || !bytes.Contains(data, []byte("bin.install \"brain\"")) || !bytes.Contains(data, []byte("on_macos")) || !bytes.Contains(data, []byte("on_linux")) {
		t.Fatal(string(data))
	}
	data, err = os.ReadFile(filepath.Join(output, "bucket/brain.json"))
	if err != nil {
		t.Fatal(err)
	}
	var scoop struct {
		Version, License, Bin string
		Architecture          map[string]map[string]string
	}
	if json.Unmarshal(data, &scoop) != nil || scoop.Version != "0.1.0-dev" || scoop.License != "MIT" || scoop.Bin != "brain.exe" || scoop.Architecture["64bit"]["url"] != releaseURL+"/brain_0.1.0-dev_windows_amd64.zip" || scoop.Architecture["64bit"]["hash"] != fmt.Sprintf("%064x", 3) {
		t.Fatal(string(data))
	}
	for _, url := range []string{"https://example.test/release", "http://github.com/a/b/releases/download/v1", "https://github.com/a/b/releases/download/v1?x", "https://github.com/a/b/releases/download/../", "https://github.com/a/b/releases/download/v1#x"} {
		if err := validateManifestInputs(url, "MIT"); err == nil {
			t.Fatal(url)
		}
	}
	if err := validateManifestInputs(releaseURL, "MIT\"; system \"bad"); err == nil {
		t.Fatal("unsafe license accepted")
	}
	if err := Manifests(t.TempDir(), releaseURL, "MIT", false); err == nil {
		t.Fatal("missing checksum accepted")
	}
	if err := os.WriteFile(filepath.Join(output, "brain_0.1.0-dev_checksums.txt"), []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Manifests(output, releaseURL, "MIT", true); err == nil {
		t.Fatal("broken checksums accepted")
	}
	var log bytes.Buffer
	for _, args := range [][]string{{"--license", "MIT"}, {"--release-url", releaseURL}, {"--release-url", "http://invalid", "--license", "MIT"}} {
		if code := Run(output, args, &log, &log); code != 2 {
			t.Fatal(code, args, log.String())
		}
	}
	t.Log("owned fixture manifest URLs and exact hashes verified; no package manager ran or installed files")
}
