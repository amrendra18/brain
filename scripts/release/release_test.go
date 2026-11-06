package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"brain/tests/testutil"
)

func root(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCrossBuildFiveTargets(t *testing.T) {
	if os.Getenv("BRAIN_PORTABLE_ONCE") == "completed" {
		t.Skip("five builds already ran in this complete lane")
	}
	binaries, err := Build(root(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(binaries) != 5 {
		t.Fatal(binaries)
	}
	for i, path := range binaries {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		stamp, err := buildinfo.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		settings := map[string]string{}
		for _, item := range stamp.Settings {
			settings[item.Key] = item.Value
		}
		if settings["GOOS"]+"/"+settings["GOARCH"] != Targets[i] || settings["CGO_ENABLED"] != "0" || settings["-trimpath"] != "true" {
			t.Fatal(settings)
		}
		t.Logf("%s: %d bytes, maximum below 30000000", Targets[i], info.Size())
	}
}

func TestPackageArchives(t *testing.T) {
	output := filepath.Join(t.TempDir(), "package")
	var log bytes.Buffer
	if code := Run(root(t), []string{"--development", "--output", output}, &log, &log); code != 0 {
		t.Fatal(code, log.String())
	}
	checksums, err := os.ReadFile(filepath.Join(output, "brain_0.1.0-dev_checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	license, err := os.ReadFile(filepath.Join(root(t), "LICENSE"))
	if err != nil || !bytes.HasPrefix(license, []byte("MIT License\n")) || !regexp.MustCompile(`(?m)^Copyright \(c\) [0-9]{4} \S`).Match(license) {
		t.Fatal("the MIT license with a copyright line is missing", err)
	}
	lines := strings.Split(strings.TrimSpace(string(checksums)), "\n")
	if len(lines) != 5 {
		t.Fatal(string(checksums))
	}
	for i, line := range lines {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			t.Fatal(line)
		}
		pair := strings.Split(Targets[i], "/")
		suffix := ".tar.gz"
		if pair[0] == "windows" {
			suffix = ".zip"
		}
		name := "brain_0.1.0-dev_" + pair[0] + "_" + pair[1] + suffix
		if parts[1] != name {
			t.Fatal(line)
		}
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != parts[0] {
			t.Fatal("checksum mismatch", name)
		}
		files := unpack(t, data, suffix)
		binary := "brain"
		if pair[0] == "windows" {
			binary += ".exe"
		}
		if len(files) != 5 || !bytes.Equal(files["LICENSE"], license) || len(files[binary]) < 1000000 || !bytes.Contains(files["README.md"], []byte("Brain")) || !bytes.Contains(files["RELEASE-NOTES.md"], []byte("Development fixture only")) || !bytes.Contains(files["THIRD-PARTY-NOTICES.txt"], []byte("modernc.org/sqlite@v1.60.1/LICENSE")) {
			t.Fatal(name, files)
		}
		if Targets[i] == runtime.GOOS+"/"+runtime.GOARCH {
			executable := filepath.Join(t.TempDir(), binary)
			if err := os.WriteFile(executable, files[binary], 0700); err != nil {
				t.Fatal(err)
			}
			stamp, err := exec.Command(executable, "--version").Output()
			if err != nil || !bytes.Contains(stamp, []byte(`"version":"0.1.0-dev"`)) {
				t.Fatal("development archive binary version", err, string(stamp))
			}
		}
		for _, word := range []string{"unsigned", "Open Anyway", "xattr -d com.apple.quarantine", "Run anyway", "Smart App Control", "licensed under MIT", "LICENSE contains its terms"} {
			if !bytes.Contains(files["RELEASE-NOTES.md"], []byte(word)) {
				t.Fatal(word)
			}
		}
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 7 {
		t.Fatal(entries, err)
	}
	if err := Pack(root(t), output, true, &log); err == nil {
		t.Fatal("existing package overwritten")
	}
	t.Log("all five archives, modes, checksums, exact MIT license, pinned module notices and unsigned development notes verified")
}

func TestTaggedFixtureBinaryVersion(t *testing.T) {
	fixture := t.TempDir()
	for name, data := range map[string][]byte{
		".gitignore":        []byte(".local/\n"),
		"go.mod":            []byte("module brain\n\ngo 1.27.1\n"),
		"cmd/brain/main.go": []byte("package main\nimport (\"encoding/json\"; \"os\"; \"brain/internal/version\")\nfunc main() { json.NewEncoder(os.Stdout).Encode(version.Current()) }\n"),
	} {
		path := filepath.Join(fixture, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	versionSource, err := os.ReadFile(filepath.Join(root(t), "internal/version/version.go"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture, "internal/version/version.go")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, versionSource, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "tagged fixture"}, {"tag", "v0.1.0"}} {
		command := exec.Command("git", args...)
		command.Dir = fixture
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatal(args, err, string(data))
		}
	}
	label, _, err := Validate(fixture, false)
	if err != nil || label != "0.1.0" {
		t.Fatal(label, err)
	}
	binaries, err := buildTargets(fixture, t.TempDir(), label, []string{runtime.GOOS + "/" + runtime.GOARCH})
	if err != nil {
		t.Fatal(err)
	}
	data, err := exec.Command(binaries[0], "--version").Output()
	if err != nil || !bytes.Contains(data, []byte(`"version":"0.1.0"`)) {
		t.Fatal("tagged binary version", err, string(data))
	}
}

func unpack(t *testing.T, data []byte, suffix string) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	if suffix == ".zip" {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range reader.File {
			r, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			result[file.Name], err = io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	} else {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		tarReader := tar.NewReader(reader)
		for {
			header, err := tarReader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			result[header.Name], err = io.ReadAll(tarReader)
			if err != nil {
				t.Fatal(err)
			}
			if header.Name == "brain" && header.Mode != 0755 {
				t.Fatal("binary lost execute mode")
			}
		}
	}
	return result
}

func TestReleaseGateAndFailures(t *testing.T) {
	fixture := t.TempDir()
	testutil.Repository(t, fixture, "Release fixture")
	var output bytes.Buffer
	if code := Run(fixture, []string{"--unknown"}, &output, &output); code != 2 {
		t.Fatal(code)
	}
	if code := Run(fixture, []string{"extra"}, &output, &output); code != 2 {
		t.Fatal(code)
	}
	if _, _, err := Validate(t.TempDir(), true); err == nil {
		t.Fatal("non-checkout accepted")
	}
	if _, _, err := Validate(fixture, false); err == nil {
		t.Fatal("untagged release accepted")
	}
	head, err := git(fixture, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	label, revision, err := validateTag("unrelated\nv0.1.0\n", head)
	if err != nil || label != "0.1.0" || revision != head {
		t.Fatal(label, revision, err)
	}
	if _, _, err := validateTag("v0.2.0", head); err == nil {
		t.Fatal("wrong version accepted")
	}
	if err := os.WriteFile(filepath.Join(fixture, "dirty.txt"), []byte("owned fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Validate(fixture, false); err == nil {
		t.Fatal("dirty release accepted")
	}
	if label, _, err := Validate(fixture, true); err != nil || label != "0.1.0-dev" {
		t.Fatal(label, err)
	}
	path := filepath.Join(t.TempDir(), "failed-package")
	if code := Run(fixture, []string{"--development", "--output", path}, &output, &output); code != 1 {
		t.Fatal(code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed build left partial release", err)
	}
	if _, err := Notices(t.TempDir()); err == nil {
		t.Fatal("missing vendor accepted")
	}
	if !strings.Contains(Notes("0.1.0", head, false), "clean version tag") {
		t.Fatal("release notes mislabeled")
	}
}

func TestDevelopmentPackagingWithoutCommit(t *testing.T) {
	fixture := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = fixture
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	label, revision, err := Validate(fixture, true)
	if err != nil || label != "0.1.0-dev" || revision != "" {
		t.Fatal("a repository with no commit was refused for development packaging", label, revision, err)
	}
	if _, _, err := Validate(fixture, false); err == nil {
		t.Fatal("a release was accepted without a commit")
	}
}
