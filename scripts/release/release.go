// Package release builds and packs local artifacts. It never publishes them.
package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"brain/internal/version"
	"brain/scripts/toolchain"
)

var Targets = []string{"darwin/arm64", "darwin/amd64", "windows/amd64", "linux/amd64", "linux/arm64"}

func Run(root string, args []string, output, stderr io.Writer) int {
	flags := flag.NewFlagSet("package", flag.ContinueOnError)
	flags.SetOutput(stderr)
	development := flags.Bool("development", false, "label a development fixture")
	destination := flags.String("output", ".local/package", "new output folder")
	releaseURL := flags.String("release-url", "", "GitHub release download URL for package-manager files")
	license := flags.String("license", "", "SPDX license ID for package-manager files")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	if (*releaseURL == "") != (*license == "") {
		fmt.Fprintln(stderr, "--release-url and --license must be supplied together")
		return 2
	}
	if *releaseURL != "" {
		if err := validateManifestInputs(*releaseURL, *license); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	path := *destination
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if err := Pack(root, path, *development, output); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *releaseURL != "" {
		if err := Manifests(path, *releaseURL, *license, *development); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}

func git(root string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-c", "core.fsmonitor=false", "-C", root}, args...)...)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GIT_") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0")
	data, err := command.Output()
	return strings.TrimSpace(string(data)), err
}

// Validate separates labelled development artifacts from clean tagged releases.
func Validate(root string, development bool) (string, string, error) {
	revision, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		inside, insideErr := git(root, "rev-parse", "--is-inside-work-tree")
		commits, commitsErr := git(root, "rev-list", "-n", "1", "--all")
		if insideErr != nil || inside != "true" || commitsErr != nil || commits != "" {
			return "", "", errors.New("packaging needs a Git checkout")
		}
		if !development {
			return "", "", errors.New("release packaging needs a tagged commit")
		}
		return version.Development, "", nil
	}
	if development {
		return version.Development, revision, nil
	}
	status, err := git(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil || status != "" {
		return "", "", errors.New("release packaging requires a clean checkout; use --development for local fixtures")
	}
	tags, err := git(root, "tag", "--points-at", "HEAD")
	if err != nil {
		return "", "", errors.New("cannot read release tags")
	}
	return validateTag(tags, revision)
}

func validateTag(tags, revision string) (string, string, error) {
	for _, tag := range strings.Fields(tags) {
		if tag == "v"+version.Declared {
			return version.Declared, revision, nil
		}
	}
	return "", "", errors.New("release packaging requires the declared version tag on HEAD")
}

// Build uses pinned Go for all five targets, without a C compiler.
func Build(root, output string) ([]string, error) {
	return buildVersion(root, output, version.Development)
}

func buildVersion(root, output, label string) ([]string, error) {
	return buildTargets(root, output, label, Targets)
}

func buildTargets(root, output, label string, targets []string) ([]string, error) {
	goPath := toolchain.Binary()
	var binaries []string
	for _, target := range targets {
		pair := strings.Split(target, "/")
		name := "brain"
		if pair[0] == "windows" {
			name += ".exe"
		}
		destination := filepath.Join(output, pair[0]+"-"+pair[1], name)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return nil, err
		}
		command := exec.Command(goPath, "build", "-trimpath", "-mod=readonly", "-buildvcs=true", "-ldflags=-X=brain/internal/version.buildVersion="+label, "-o", destination, "./cmd/brain")
		command.Dir = root
		for _, item := range os.Environ() {
			key := strings.SplitN(item, "=", 2)[0]
			if key != "GOOS" && key != "GOARCH" && key != "CGO_ENABLED" {
				command.Env = append(command.Env, item)
			}
		}
		command.Env = append(command.Env, "GOOS="+pair[0], "GOARCH="+pair[1], "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOWORK=off")
		if data, err := command.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("build %s: %w: %s", target, err, data)
		}
		info, err := os.Stat(destination)
		if err != nil {
			return nil, err
		}
		if info.Size() >= 30000000 {
			return nil, fmt.Errorf("%s binary is %d bytes, at or above 30000000", target, info.Size())
		}
		binaries = append(binaries, destination)
	}
	return binaries, nil
}

// Pack requires the caller's output folder to be new, avoiding stale artifacts.
func Pack(root, output string, development bool, log io.Writer) error {
	label, revision, err := Validate(root, development)
	if err != nil {
		return err
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		return errors.New("package output folder must not exist")
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(output)
		}
	}()
	buildFolder := filepath.Join(output, "build")
	binaries, err := buildVersion(root, buildFolder, label)
	if err != nil {
		return err
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return err
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return err
	}
	notices, err := Notices(root)
	if err != nil {
		return err
	}
	notes := Notes(label, revision, development)
	var checksums strings.Builder
	for i, target := range Targets {
		stamp, err := buildinfo.ReadFile(binaries[i])
		if err != nil {
			return err
		}
		settings := map[string]string{}
		for _, setting := range stamp.Settings {
			settings[setting.Key] = setting.Value
		}
		if settings["vcs.revision"] != revision || (!development && settings["vcs.modified"] != "false") {
			return errors.New("binary build stamp does not match the selected release")
		}
		binary, err := os.ReadFile(binaries[i])
		if err != nil {
			return err
		}
		pair := strings.Split(target, "/")
		name := "brain_" + label + "_" + pair[0] + "_" + pair[1]
		files := []entry{{filepath.Base(binaries[i]), binary, 0755}, {"README.md", readme, 0644}, {"LICENSE", license, 0644}, {"RELEASE-NOTES.md", []byte(notes), 0644}, {"THIRD-PARTY-NOTICES.txt", notices, 0644}}
		var data []byte
		if pair[0] == "windows" {
			name += ".zip"
			data, err = zipArchive(files)
		} else {
			name += ".tar.gz"
			data, err = tarArchive(files)
		}
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(output, name), data, 0600); err != nil {
			return err
		}
		fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256(data), name)
		fmt.Fprintf(log, "%s: %d bytes\n", target, len(binary))
	}
	if err = os.RemoveAll(buildFolder); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(output, "brain_"+label+"_checksums.txt"), []byte(checksums.String()), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(output, "RELEASE-NOTES.md"), []byte(notes), 0600); err != nil {
		return err
	}
	if !development {
		currentLabel, currentRevision, err := Validate(root, false)
		if err != nil || currentLabel != label || currentRevision != revision {
			return errors.New("release checkout changed while packaging")
		}
	}
	fmt.Fprint(log, notes)
	complete = true
	return nil
}

func Notes(label, revision string, development bool) string {
	status := "Release artifacts built from a clean version tag."
	if development {
		status = "Development fixture only. Do not publish these artifacts. The binary reports " + version.Development + "."
	}
	return fmt.Sprintf("# Brain %s\n\n%s\nRevision: %s\n\nThese binaries are unsigned and are not notarised.\nOn macOS, use System Settings, Privacy & Security, Open Anyway.\nFor a trusted direct download, xattr -d com.apple.quarantine ./brain removes its quarantine flag.\nOn Windows, choose More info, then Run anyway when that option is available.\nEnterprise policy may prevent that choice. Windows 11 Smart App Control can block unsigned programs. Signing is the remedy.\nLinux needs no platform signing step.\n\nVerify the archive with brain_%s_checksums.txt before unpacking.\nRun brain --version and match its revision before using it.\nmacOS requires version 13 or newer. Windows and Linux native tests remain unproved until the five-runner workflow completes.\nBrain-authored code is licensed under MIT. LICENSE contains its terms and copyright notice.\nThird-party license notices are included in every archive.\n", label, status, revision, label)
}

func Notices(root string) ([]byte, error) {
	command := exec.Command(toolchain.Binary(), "mod", "edit", "-json")
	command.Dir = root
	command.Env = append(os.Environ(), "GOENV=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("read pinned modules: %w: %s", err, exit.Stderr)
		}
		return nil, fmt.Errorf("read pinned modules: %w", err)
	}
	cacheCommand := exec.Command(command.Path, "env", "GOMODCACHE")
	cacheCommand.Dir = root
	cacheCommand.Env = command.Env
	cacheOutput, err := cacheCommand.Output()
	if err != nil {
		return nil, fmt.Errorf("find pinned module cache: %w", err)
	}
	cache := strings.TrimSpace(string(cacheOutput))
	var manifest struct {
		Require []struct {
			Path, Version string
			Indirect      bool
		}
	}
	if err := json.Unmarshal(output, &manifest); err != nil {
		return nil, err
	}
	if len(manifest.Require) == 0 {
		return nil, errors.New("pinned module list is empty")
	}
	sort.Slice(manifest.Require, func(i, j int) bool { return manifest.Require[i].Path < manifest.Require[j].Path })
	var result strings.Builder
	for _, required := range manifest.Require {
		download := exec.Command(command.Path, "mod", "download", "-json", required.Path+"@"+required.Version)
		download.Dir = root
		download.Env = command.Env
		data, err := download.Output()
		if err != nil {
			return nil, fmt.Errorf("download pinned module %s@%s: %w", required.Path, required.Version, err)
		}
		var module struct {
			Path, Version, Dir, Error string
		}
		if err := json.Unmarshal(data, &module); err != nil {
			return nil, err
		}
		if module.Error != "" || module.Path != required.Path || module.Version != required.Version {
			return nil, fmt.Errorf("pinned module %s@%s could not be verified: %s", required.Path, required.Version, module.Error)
		}
		if module.Dir == "" {
			return nil, fmt.Errorf("pinned module %s@%s is absent from the module cache", module.Path, module.Version)
		}
		relativeToCache, err := filepath.Rel(cache, module.Dir)
		if err != nil || relativeToCache == ".." || strings.HasPrefix(relativeToCache, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeToCache) {
			return nil, fmt.Errorf("pinned module %s@%s is outside the module cache", module.Path, module.Version)
		}
		found := false
		err = filepath.WalkDir(module.Dir, func(path string, item os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if item.IsDir() || !item.Type().IsRegular() || !strings.HasPrefix(item.Name(), "LICENSE") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(module.Dir, path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&result, "%s@%s/%s\n\n%s\n\n", module.Path, module.Version, filepath.ToSlash(relative), data)
			found = true
			return nil
		})
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("pinned module %s@%s has no license notice", module.Path, module.Version)
		}
	}
	return []byte(result.String()), nil
}

type entry struct {
	name string
	data []byte
	mode int64
}

func tarArchive(files []entry) ([]byte, error) {
	var buffer strings.Builder
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, file := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.data)), ModTime: time.Unix(0, 0)}); err != nil {
			return nil, err
		}
		if _, err := tarWriter.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	return []byte(buffer.String()), nil
}

func zipArchive(files []entry) ([]byte, error) {
	var buffer strings.Builder
	writer := zip.NewWriter(&buffer)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetMode(os.FileMode(file.mode))
		header.Modified = time.Unix(0, 0).UTC()
		item, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err = item.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return []byte(buffer.String()), nil
}
