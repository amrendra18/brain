package installrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"brain/internal/corpus"
	"brain/internal/redact"
)

func installPrivatePolicy(t *testing.T, f *fixture, value string) string {
	t.Helper()
	path := filepath.Join(f.state, "private-values.json")
	data, err := json.Marshal([]string{value})
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(data))
	config := corpus.Default(f.source, []string{f.source}, nil)
	config.PrivateValuesFile = path
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write(t, f.config, string(data))
	return path
}

func TestInstallProtectedInputsNeverPublish(t *testing.T) {
	for _, custom := range []bool{false, true} {
		for _, target := range []string{"binary", "home", "source", "state", "config", "raw argument"} {
			t.Run(target+map[bool]string{false: "/default", true: "/custom"}[custom], func(t *testing.T) {
				f := newFixture(t)
				secret := "sk-proj-" + strings.Repeat("Q", 48)
				if custom {
					secret = "sample-private-value-49027"
				}
				args := []string{"--write", "--no-schedule"}
				switch target {
				case "binary":
					f.binary = filepath.Join(f.home, secret, "brain")
					args = append(args, "--bin-dir", filepath.Dir(f.binary))
				case "home":
					f.home = filepath.Join(filepath.Dir(f.home), secret)
					f.installer.home = f.home
					f.binary = filepath.Join(f.home, ".local", "bin", "brain")
				case "source":
					f.source = filepath.Join(filepath.Dir(f.source), secret)
				case "state":
					f.state = filepath.Join(filepath.Dir(f.state), secret)
					f.config = filepath.Join(f.state, "config.json")
					args = append(args, "--state-dir", f.state)
				case "config":
					f.config = filepath.Join(f.state, secret+".json")
					args = append(args, "--config", f.config)
				case "raw argument":
					args = append(args, "--bin-dir", f.home+string(filepath.Separator)+secret+string(filepath.Separator)+".."+string(filepath.Separator)+".local"+string(filepath.Separator)+"bin")
				}
				f.existing(t)
				if custom {
					installPrivatePolicy(t, f, secret)
				}
				if target == "source" {
					if err := os.MkdirAll(f.source, 0700); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--source", f.source)
				}
				before := read(t, f.config)
				var out bytes.Buffer
				if code := f.installer.runInstall(args, &out); code != 1 {
					t.Fatalf("exit %d: %s", code, out.String())
				}
				if strings.Contains(out.String(), secret) || !strings.Contains(out.String(), "protected text") {
					t.Fatalf("unsafe refusal: %s", out.String())
				}
				if read(t, f.binary) != "old binary" || read(t, f.config) != before || read(t, filepath.Join(f.state, "notes", "kept.md")) != "saved note" {
					t.Fatal("protected input changed existing files")
				}
				backup := filepath.Join(f.home, ".local", "share", "brain-install-backups")
				if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("created backup before privacy preflight")
				}
			})
		}
	}
}

func TestInstallMalformedFlagsOmitInputs(t *testing.T) {
	for _, secret := range []string{"sk-proj-" + strings.Repeat("Q", 48), "sample-private-value-49027"} {
		for _, args := range [][]string{{"--write=" + secret}, {"--" + secret}, {secret}, {"--skills", secret}, {"--time", secret}} {
			f := newFixture(t)
			installPrivatePolicy(t, f, secret)
			var out bytes.Buffer
			if code := f.installer.runInstall(args, &out); code != 2 || strings.Contains(out.String(), secret) {
				t.Fatalf("exit %d: %s", code, out.String())
			}
			if len(f.calls) != 0 {
				t.Fatal("invalid flags started installation")
			}
		}
	}
}

func TestInstallPrivacyPolicyFailsClosed(t *testing.T) {
	for _, content := range []string{"missing", "invalid", `null`} {
		t.Run(content, func(t *testing.T) {
			f := newFixture(t)
			f.existing(t)
			path := installPrivatePolicy(t, f, "private-value-49027")
			if content == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				write(t, path, content)
			}
			result, code := f.install(t, "--write")
			if code != 1 || result.Binary != "" || result.Selection.State != "" || result.Backup != "" || read(t, f.binary) != "old binary" {
				t.Fatalf("policy failure published data: %+v", result)
			}
		})
	}
}

func TestInstallPartialFailureSanitizesReceipt(t *testing.T) {
	for _, custom := range []bool{false, true} {
		f := newFixture(t)
		f.existing(t)
		secret := "sk-proj-" + strings.Repeat("Q", 48)
		if custom {
			secret = "sample-private-value-49027"
			installPrivatePolicy(t, f, secret)
		}
		run := f.installer.run
		f.installer.run = func(binary string, args ...string) ([]byte, error) {
			if args[0] == "index" {
				return nil, errors.New("test command failure " + secret)
			}
			return run(binary, args...)
		}
		result, code := f.install(t, "--write")
		if code != 1 || result.Backup == "" || strings.Contains(result.Message, secret) || !strings.Contains(result.Message, "[redacted:") {
			t.Fatalf("unsafe partial result: %+v", result)
		}
		if strings.Contains(read(t, filepath.Join(result.Backup, "receipt.json")), secret) || read(t, f.binary) != "candidate binary" || read(t, filepath.Join(result.Backup, "brain.previous")) != "old binary" {
			t.Fatal("partial installation lost safe recovery data")
		}
	}
}

func TestInstallReceiptSanitizesEveryField(t *testing.T) {
	f := newFixture(t)
	secret := "sample-private-value-49027"
	policy, err := redact.Load(installPrivatePolicy(t, f, secret))
	if err != nil {
		t.Fatal(err)
	}
	result := receipt{policy: policy, Status: secret, Binary: secret, Selection: selection{State: secret, Config: secret}, Backup: secret, Completed: []string{secret}, Message: secret}
	data, err := json.Marshal(result.safe())
	if err != nil || bytes.Contains(data, []byte(secret)) || result.Completed[0] != secret {
		t.Fatalf("receipt fields or original mutated: %s %v", data, err)
	}
	if safeError(&os.PathError{Op: "open", Path: secret, Err: os.ErrPermission}) != os.ErrPermission.Error() || safeError(&os.LinkError{Op: "rename", Old: secret, New: secret, Err: os.ErrPermission}) != os.ErrPermission.Error() {
		t.Fatal("filesystem error exposes a path")
	}
}
