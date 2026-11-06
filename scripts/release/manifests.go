package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"brain/internal/version"
)

func validateManifestInputs(releaseURL, license string) error {
	parsed, err := url.Parse(releaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.RawPath != "" {
		return errors.New("--release-url must name a GitHub HTTPS release download folder")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	safe := regexp.MustCompile("^[A-Za-z0-9_.-]+$")
	if len(parts) != 5 || parts[2] != "releases" || parts[3] != "download" {
		return errors.New("--release-url must include the repository and release tag")
	}
	for _, part := range []string{parts[0], parts[1], parts[4], license} {
		if !safe.MatchString(part) || part == "." || part == ".." {
			return errors.New("release URL or license contains an invalid value")
		}
	}
	return nil
}

// Manifests writes only inside the caller's package output, with exact archive hashes.
func Manifests(output, releaseURL, license string, development bool) error {
	if err := validateManifestInputs(releaseURL, license); err != nil {
		return err
	}
	label := version.Declared
	if development {
		label += "-dev"
	}
	checksums, err := os.ReadFile(filepath.Join(output, "brain_"+label+"_checksums.txt"))
	if err != nil {
		return err
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(checksums)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 || !regexp.MustCompile("^[a-f0-9]{64}$").MatchString(parts[0]) {
			return errors.New("package checksums are incomplete")
		}
		hashes[parts[1]] = parts[0]
	}
	base := strings.TrimRight(releaseURL, "/")
	homepage := strings.Split(base, "/releases/download/")[0]
	var formula strings.Builder
	fmt.Fprintf(&formula, "class Brain < Formula\n  desc \"Search local notes and project documents\"\n  homepage %q\n  version %q\n  license %q\n", homepage, label, license)
	for _, system := range []string{"darwin", "linux"} {
		scope := "macos"
		if system == "linux" {
			scope = "linux"
		}
		fmt.Fprintf(&formula, "  on_%s do\n", scope)
		if system == "darwin" {
			formula.WriteString("    depends_on macos: :ventura\n")
		}
		for _, architecture := range []string{"arm64", "amd64"} {
			name := "brain_" + label + "_" + system + "_" + architecture + ".tar.gz"
			hash := hashes[name]
			if hash == "" {
				return errors.New("package archive checksum is missing")
			}
			block := "arm"
			if architecture == "amd64" {
				block = "intel"
			}
			fmt.Fprintf(&formula, "    on_%s do\n      url %q\n      sha256 %q\n    end\n", block, base+"/"+name, hash)
		}
		formula.WriteString("  end\n")
	}
	formula.WriteString("  def install\n    bin.install \"brain\"\n  end\n  test do\n    assert_match version.to_s.sub(/-dev$/, \"\"), shell_output(\"#{bin}/brain --version\")\n  end\nend\n")
	name := "brain_" + label + "_windows_amd64.zip"
	hash := hashes[name]
	if hash == "" {
		return errors.New("Windows archive checksum is missing")
	}
	scoop := map[string]any{"version": label, "description": "Search local notes and project documents", "homepage": homepage, "license": license, "architecture": map[string]any{"64bit": map[string]string{"url": base + "/" + name, "hash": hash}}, "bin": "brain.exe", "notes": "Unsigned. Smart App Control or enterprise policy may block execution."}
	data, err := json.MarshalIndent(scoop, "", "  ")
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{"Formula/brain.rb": []byte(formula.String()), "bucket/brain.json": append(data, '\n')} {
		path := filepath.Join(output, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
	}
	return nil
}
