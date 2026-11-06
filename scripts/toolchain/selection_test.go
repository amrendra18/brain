package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func selectionFixture(t *testing.T, name, version string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	program := "#!/bin/sh\n" +
		"if [ \"$GOENV\" != off ] || [ \"$GOTOOLCHAIN\" != local ]; then echo 'unsafe Go environment' >&2; exit 40; fi\n" +
		"printf '%s\\n' '" + name + "' >> \"$GO_SELECTION_LOG\"\n" +
		"if [ \"$1\" = env ] && [ \"$2\" = GOROOT ]; then printf '%s\\n' \"$FAKE_GO_ROOT\"; exit 0; fi\n" +
		"if [ \"$1\" = version ]; then printf '%s\\n' 'go version " + version + " darwin/arm64'; exit 0; fi\n" +
		"exit 9\n"
	for file, text := range map[string]string{"go": program, "gofmt": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(bin, file), []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runSelection(t *testing.T, checkout, pathGo, fakeRoot, log string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Git Bash path conversion requires native Windows evidence")
	}
	script, err := filepath.Abs("../go-env")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `. "$BRAIN_ENV_SCRIPT"; printf 'SELECTED=%s\nROOT=%s\nTOOLCHAIN=%s\n' "$BRAIN_GO_BIN" "$GOROOT" "$GOTOOLCHAIN"`)
	cmd.Env = append(cleanGoEnv(), "BRAIN_ENV_SCRIPT="+script, "repo_root="+checkout,
		"PATH="+filepath.Join(pathGo, "bin")+":/usr/bin:/bin", "FAKE_GO_ROOT="+fakeRoot,
		"GO_SELECTION_LOG="+log, "GOROOT=/wrong/inherited/root")
	data, err := cmd.CombinedOutput()
	return string(data), err
}

func cleanGoEnv() []string {
	var result []string
	for _, item := range os.Environ() {
		switch strings.SplitN(item, "=", 2)[0] {
		case "GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT", "BRAIN_GO_BIN":
		default:
			result = append(result, item)
		}
	}
	return result
}

func TestGoSelectionDefault(t *testing.T) {
	checkout := t.TempDir()
	goRoot := selectionFixture(t, "default", "go1.27.1")
	log := filepath.Join(t.TempDir(), "calls")
	output, err := runSelection(t, checkout, goRoot, goRoot, log)
	if err != nil || !strings.Contains(output, "SELECTED="+filepath.Join(goRoot, "bin/go")) ||
		!strings.Contains(output, "ROOT="+goRoot) || !strings.Contains(output, "TOOLCHAIN=local") {
		t.Fatal(err, output)
	}
}

func TestGoSelectionOverride(t *testing.T) {
	checkout := t.TempDir()
	pathGo := selectionFixture(t, "wrong-path", "go1.26.0")
	chosen := selectionFixture(t, "Go with spaces", "go1.27.1")
	if err := os.MkdirAll(filepath.Join(checkout, ".local"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".local/go-root"), []byte(chosen+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "calls")
	output, err := runSelection(t, checkout, pathGo, chosen, log)
	if err != nil || !strings.Contains(output, "SELECTED="+filepath.Join(chosen, "bin/go")) {
		t.Fatal(err, output)
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.Contains(string(data), "wrong-path") || !strings.Contains(string(data), "Go with spaces") {
		t.Fatal(err, string(data))
	}
}

func TestGoSelectionRefusals(t *testing.T) {
	pathGo := selectionFixture(t, "valid-path", "go1.27.1")
	for _, selection := range []string{"relative/go", "/missing/go"} {
		checkout := t.TempDir()
		if err := os.MkdirAll(filepath.Join(checkout, ".local"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(checkout, ".local/go-root"), []byte(selection+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		output, err := runSelection(t, checkout, pathGo, pathGo, filepath.Join(t.TempDir(), "calls"))
		if err == nil || !strings.Contains(output, ".local/go-root") && !strings.Contains(output, "selected Go installation") {
			t.Fatal(selection, err, output)
		}
	}
	checkout := t.TempDir()
	wrong := selectionFixture(t, "wrong-version", "go1.26.0")
	output, err := runSelection(t, checkout, wrong, wrong, filepath.Join(t.TempDir(), "calls"))
	if err == nil || !strings.Contains(output, "must be Go 1.27.1") {
		t.Fatal(err, output)
	}
}

func TestGoSelectionWindowsPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows execution belongs to its runner")
	}
	chosen := selectionFixture(t, "Windows Go", "go1.27.1")
	for _, name := range []string{"go", "gofmt"} {
		if err := os.Rename(filepath.Join(chosen, "bin", name), filepath.Join(chosen, "bin", name+".exe")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(chosen, "bin/go"), []byte("#!/bin/sh\necho 'extensionless go was called' >&2\nexit 83\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	cygpath := "#!/bin/sh\nif [ \"$1\" = -u ]; then printf '%s\\n' \"$2\"; else printf 'C:%s\\n' \"$2\"; fi\n"
	if err := os.WriteFile(filepath.Join(tools, "cygpath"), []byte(cygpath), 0700); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../go-env")
	if err != nil {
		t.Fatal(err)
	}
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "command-path", true: "project-override"}[override], func(t *testing.T) {
			checkout := t.TempDir()
			if override {
				if err := os.MkdirAll(filepath.Join(checkout, ".local"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(checkout, ".local/go-root"), []byte(chosen+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("sh", "-c", `. "$BRAIN_ENV_SCRIPT"; printf 'GO=%s\nROOT=%s\nCACHE=%s\n' "$BRAIN_GO_BIN" "$GOROOT" "$GOCACHE"`)
			cmd.Env = append(cleanGoEnv(), "BRAIN_ENV_SCRIPT="+script, "repo_root="+checkout,
				"PATH="+tools+":"+filepath.Join(chosen, "bin")+":/usr/bin:/bin", "FAKE_GO_ROOT="+chosen,
				"GO_SELECTION_LOG="+filepath.Join(t.TempDir(), "calls"))
			data, err := cmd.CombinedOutput()
			output := string(data)
			if err != nil || !strings.Contains(output, "GO=C:"+filepath.Join(chosen, "bin/go.exe")) ||
				!strings.Contains(output, "ROOT=C:"+chosen) ||
				!strings.Contains(output, "CACHE=C:"+filepath.Join(checkout, ".local/go-build-cache")) ||
				strings.Contains(output, "extensionless go was called") {
				t.Fatal(err, output)
			}
		})
	}
}

func TestGoBinarySelection(t *testing.T) {
	selected := filepath.Join(t.TempDir(), "selected go")
	t.Setenv("BRAIN_GO_BIN", selected)
	if got := Binary(); got != selected {
		t.Fatalf("selected compiler: got %q, want %q", got, selected)
	}
	t.Setenv("BRAIN_GO_BIN", "")
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	want := filepath.Join(runtime.GOROOT(), "bin", name)
	if got := Binary(); got != want {
		t.Fatalf("direct test compiler: got %q, want %q", got, want)
	}
}
